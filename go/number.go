// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// Number text, shared by the renderers.
//
// A number reaches a renderer as a machine value and, when the source
// could hand it over, the lexeme it was spelled with. The lexeme wins,
// because it is the only thing that keeps `50.25` as `50.25` and keeps the
// digits of a number beyond float64's exact range; but a lexeme is data
// from a source, so it is checked against the JSON number grammar before
// it is copied into an output that promises to be JSON or CSV. Without a
// lexeme the shortest text that reads back as the same float64 is
// written. A value with no finite text (NaN, infinity) is rejected as
// unrepresentable rather than written as null, and it is rejected
// whatever lexeme stands beside it.
//
// Validation and formatting are separate functions so a renderer can
// check a whole row before it writes any of it, and format each number
// once.

import (
	"fmt"
	"math"
	"strconv"

	tt "github.com/tabnas/transduce/go"
)

// IsJSONNumber reports whether text is a number by RFC 8259's grammar:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?, nothing else and
// nothing around it.
func IsJSONNumber(text string) bool {
	i, n := 0, len(text)
	digits := func() int {
		start := i
		for i < n && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < n && text[i] == '-' {
		i++
	}
	switch {
	case i < n && text[i] == '0':
		i++
	case i < n && text[i] >= '1' && text[i] <= '9':
		digits()
	default:
		return false
	}
	if i < n && text[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < n && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < n && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == n
}

// checkNumber reports whether a renderer may write this number at all.
//
// A lexeme that is not a JSON number is INVALID_NUMBER. A value that is
// not finite is TARGET_VALUE_UNREPRESENTABLE, with or without a lexeme.
// Nothing is formatted here, so a renderer can run this over a whole row
// before writing a byte of it.
//
// transduce's Go types carry "no lexeme" as the empty string, so an empty
// lexeme is no lexeme here: the one place this runtime differs from the
// Rust crate, where Some("") is a lexeme and INVALID_NUMBER (see
// DIVERGENCE.md).
func checkNumber(value float64, lexeme string) *tt.Fail {
	if lexeme != "" && !IsJSONNumber(lexeme) {
		return tt.NewFail(tt.CodeInvalidNumber, fmt.Sprintf("%s is not a JSON number", strconv.Quote(lexeme)))
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		var message string
		if lexeme != "" {
			message = fmt.Sprintf("%s is %s as a number, which has no representation", strconv.Quote(lexeme), nonFinite(value))
		} else {
			message = fmt.Sprintf("%s has no representation as a number", nonFinite(value))
		}
		return tt.NewFail(tt.CodeTargetValueUnrepresentable, message)
	}
	return nil
}

// nonFinite is a non-finite value as Rust's Display spells it.
func nonFinite(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case v > 0:
		return "inf"
	}
	return "-inf"
}

// The magnitudes written positionally: from 1e-6 up to, not including,
// 1e21. These are the thresholds JavaScript's Number#toString uses, so
// they are the ones most JSON in circulation was written with; an integer
// of up to 21 digits stays an integer, and 1e300 is five characters
// rather than 301.
const (
	positionalMin = 1e-6
	positionalMax = 1e21
)

// appendValue appends the shortest text that reads back as value to dst.
//
// strconv's shortest formatting gives the digits; this function only
// chooses the layout, because no Go verb has the fixture's: positional
// for zero and for magnitudes within [positionalMin, positionalMax),
// exponent form outside with no '+' and no leading zeros in the exponent
// (1e21, 1e-7, 1.7976931348623157e308; Go's 'e' writes 1e+21 and 1e-07).
// Negative zero is "-0". The value must be finite, which checkNumber
// establishes before every call.
func appendValue(dst []byte, value float64) []byte {
	if value == 0 {
		if math.Signbit(value) {
			return append(dst, '-', '0')
		}
		return append(dst, '0')
	}
	// d.ddddde±XX: the shortest round-tripping digits and the exponent.
	var scratch [32]byte
	sci := strconv.AppendFloat(scratch[:0], value, 'e', -1, 64)
	neg := sci[0] == '-'
	if neg {
		sci = sci[1:]
	}
	mark := len(sci) - 1
	for sci[mark] != 'e' {
		mark--
	}
	e, _ := strconv.Atoi(string(sci[mark+1:]))
	var digits [24]byte
	nd := 0
	for _, c := range sci[:mark] {
		if c != '.' {
			digits[nd] = c
			nd++
		}
	}
	ds := digits[:nd]
	if neg {
		dst = append(dst, '-')
	}
	magnitude := math.Abs(value)
	if magnitude >= positionalMin && magnitude < positionalMax {
		switch {
		case e >= nd-1:
			dst = append(dst, ds...)
			for k := 0; k < e-(nd-1); k++ {
				dst = append(dst, '0')
			}
		case e >= 0:
			dst = append(dst, ds[:e+1]...)
			dst = append(dst, '.')
			dst = append(dst, ds[e+1:]...)
		default:
			dst = append(dst, '0', '.')
			for k := 0; k < -e-1; k++ {
				dst = append(dst, '0')
			}
			dst = append(dst, ds...)
		}
		return dst
	}
	dst = append(dst, ds[0])
	if nd > 1 {
		dst = append(dst, '.')
		dst = append(dst, ds[1:]...)
	}
	dst = append(dst, 'e')
	return strconv.AppendInt(dst, int64(e), 10)
}

// FormatValue is the text a renderer writes for a finite value with no
// lexeme: the shortest digits that read back as the same float64, laid
// out positionally within [1e-6, 1e21) and in exponent form outside. A
// non-finite value has no such text and gives "".
func FormatValue(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	return string(appendValue(nil, value))
}
