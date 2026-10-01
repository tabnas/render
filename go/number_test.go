// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

import (
	"math"
	"strconv"
	"strings"
	"testing"

	tt "github.com/tabnas/transduce/go"
)

func TestTheJSONNumberGrammarIsExact(t *testing.T) {
	for _, ok := range []string{
		"0", "-0", "1", "-1", "10", "1.5", "0.0", "1e5", "1E5", "1e+5", "1e-5",
		"1.5e10", "123456789012345678901234567890", "-0.000001",
	} {
		if !IsJSONNumber(ok) {
			t.Errorf("%q should be a number", ok)
		}
	}
	for _, bad := range []string{
		"", "-", "+1", "01", "1.", ".5", "1e", "1e+", "1.e5", "0x10", "NaN",
		"Infinity", "-Infinity", " 1", "1 ", "1_000", "1,5", "١",
	} {
		if IsJSONNumber(bad) {
			t.Errorf("%q should not be a number", bad)
		}
	}
}

func TestAJSONNumberLexemeBesideAFiniteValuePasses(t *testing.T) {
	for _, c := range []struct {
		v float64
		l string
	}{{1, "1.00"}, {1, ""}, {1e30, "123456789012345678901234567890"}} {
		if f := checkNumber(c.v, c.l); f != nil {
			t.Errorf("%v %q: %v", c.v, c.l, f)
		}
	}
}

func TestALexemeThatIsNotAJSONNumberIsInvalidNumber(t *testing.T) {
	if f := checkNumber(1, "1."); f == nil || f.Code != tt.CodeInvalidNumber {
		t.Errorf("1.: %v", f)
	}
	// The lexeme is judged first: a NaN spelled "NaN" is a bad lexeme, not
	// an unrepresentable value.
	if f := checkNumber(math.NaN(), "NaN"); f == nil || f.Code != tt.CodeInvalidNumber {
		t.Errorf("NaN: %v", f)
	}
}

func TestANonFiniteValueIsUnrepresentableWithOrWithoutALexeme(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if f := checkNumber(v, ""); f == nil || f.Code != tt.CodeTargetValueUnrepresentable {
			t.Errorf("%v: %v", v, f)
		}
	}
	f := checkNumber(math.Inf(1), "1e999")
	if f == nil || f.Code != tt.CodeTargetValueUnrepresentable || !strings.Contains(f.Message, `"1e999"`) {
		t.Errorf("1e999: %v", f)
	}
	if f := checkNumber(math.Inf(-1), "-1e999"); f == nil || f.Code != tt.CodeTargetValueUnrepresentable {
		t.Errorf("-1e999: %v", f)
	}
}

func TestAValueTakesTheShortestFormPositionalWithinTheJavaScriptRange(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{1, "1"}, {0.1, "0.1"}, {math.Copysign(0, -1), "-0"}, {0, "0"}, {50.25, "50.25"},
		{123456.789, "123456.789"}, {1e20, "100000000000000000000"},
		{1.5e17, "150000000000000000"}, {1.23456789e18, "1234567890000000000"},
		{0.000001, "0.000001"}, {-0.000025, "-0.000025"},
	} {
		if got := FormatValue(c.v); got != c.want {
			t.Errorf("%v: got %q, want %q", c.v, got, c.want)
		}
	}
}

func TestAValueOutsideThePositionalRangeTakesTheExponentForm(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{1e21, "1e21"}, {1e300, "1e300"}, {1e-300, "1e-300"}, {1e-7, "1e-7"},
		{-2.5e-8, "-2.5e-8"}, {5e-324, "5e-324"}, {math.MaxFloat64, "1.7976931348623157e308"},
		{-1.5e300, "-1.5e300"},
	} {
		if got := FormatValue(c.v); got != c.want {
			t.Errorf("%v: got %q, want %q", c.v, got, c.want)
		}
	}
}

func TestEveryFormIsAJSONNumberThatReadsBackAsTheSameValue(t *testing.T) {
	values := []float64{
		0, math.Copysign(0, -1), 1, 0.1, 1e20, 1e21, 1e300, 1e-300, 1e-6, 1e-7, 5e-324,
		math.MaxFloat64, -math.MaxFloat64, 2.2250738585072014e-308, 123456.789,
		math.Pow(2, 53), 9.999999999999999e20,
	}
	for _, v := range values {
		text := FormatValue(v)
		if !IsJSONNumber(text) {
			t.Errorf("%v wrote %q, not a JSON number", v, text)
		}
		back, err := strconv.ParseFloat(text, 64)
		if err != nil || math.Float64bits(back) != math.Float64bits(v) {
			t.Errorf("%v wrote %q, which reads back as %v (%v)", v, text, back, err)
		}
	}
	if FormatValue(math.NaN()) != "" || FormatValue(math.Inf(1)) != "" {
		t.Error("a non-finite value has no text")
	}
}

func TestTheReviewersProbeIsBytesNotHundredsOfDigits(t *testing.T) {
	total := 0
	for _, v := range []float64{1e300, 1e-300, 1.5e17, 1.23456789e18} {
		total += len(FormatValue(v))
	}
	if total != 5+6+18+19 {
		t.Errorf("total %d", total)
	}
}
