// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// TableRows/1 as CSV: the always-quoted profile.
//
// The standard profile quotes every field, doubles `"`, ends every record
// with CRLF and writes numbers as their lexemes. Always quoting makes the
// output independent of the data (no field can change the record's
// shape), makes the empty string and the null text distinguishable from a
// missing quote pair, and lets a reader tell that a field was a field.
// Minimal quoting and other delimiters are dialects the caller selects
// explicitly, valid here because a row is a finite vector: the renderer
// sees the whole field before it decides how to write it.
//
// The renderer validates the protocol as it goes, because a third-party
// transducer or a host adapter is as much a source of TableRows/1 as the
// standard table transducer is.

import (
	"fmt"
	"strconv"
	"strings"

	tt "github.com/tabnas/transduce/go"
)

// Newline is the record terminator.
type Newline uint8

const (
	// NewlineCRLF is RFC 4180's terminator, and the standard profile's.
	NewlineCRLF Newline = iota
	// NewlineLF is a dialect.
	NewlineLF
)

// String is the terminator's text.
func (n Newline) String() string {
	if n == NewlineLF {
		return "\n"
	}
	return "\r\n"
}

// Quoting is when a field is quoted.
type Quoting uint8

const (
	// QuotingAlways quotes every field: the standard profile.
	QuotingAlways Quoting = iota
	// QuotingMinimal quotes only a field holding the delimiter, `"`, CR
	// or LF. An empty field is then written as nothing, so the empty
	// string and an empty null text read back the same; that is the
	// dialect's trade-off, not a defect.
	QuotingMinimal
)

// CSVOptions is the CSV dialect. Start from DefaultCSVOptions: the zero
// value's delimiter is NUL, which no reader can take and NewCSVRenderer
// refuses.
type CSVOptions struct {
	// Delimiter is one character, and not `"`, CR, LF or NUL: those would
	// make the output unreadable by construction, and are refused when
	// the renderer is built.
	Delimiter rune
	Newline   Newline
	// Header writes the labels as the first record.
	Header bool
	// NullText is the text of a CellNull; empty by default.
	NullText string
	// Missing is what a CellMissing becomes: nil fails the run with
	// MISSING_VALUE (a table that promised a column and did not deliver
	// it is not silently padded), else the text it points at.
	Missing *string
	Quoting Quoting
}

// DefaultCSVOptions is the standard profile: `,`, CRLF, a header, an
// empty null text, Missing an error, every field quoted.
func DefaultCSVOptions() CSVOptions {
	return CSVOptions{Delimiter: ',', Newline: NewlineCRLF, Header: true}
}

// MissingAs is the CSVOptions.Missing that writes text for a CellMissing.
func MissingAs(text string) *string { return &text }

type phase uint8

const (
	beforeSchema phase = iota
	inRows
	done
)

// CSVRenderer renders TableRows/1 as CSV.
//
// One schema first, rows exactly as wide as the schema, one end: anything
// else is PROTOCOL_ORDER_ERROR. A schema with no columns has no CSV form
// (a record cannot be empty) and is TARGET_VALUE_UNREPRESENTABLE. Every
// record, the header included, ends with the configured newline, the last
// one too. The output is flushed once, at End, so a table that fails half
// way is not flushed as if it were whole; a failure found after any text
// was written says so with CommittedOutput.
type CSVRenderer[O TextOut] struct {
	out       O
	options   CSVOptions
	delimiter string
	phase     phase
	labels    []string
	rows      uint64
	emitted   bool
	scratch   []byte
}

// NewCSVRenderer is a renderer over out, or TARGET_VALUE_UNREPRESENTABLE
// when the delimiter is one no CSV reader could take.
func NewCSVRenderer[O TextOut](out O, options CSVOptions) (*CSVRenderer[O], *tt.Fail) {
	switch options.Delimiter {
	case '"', '\r', '\n', 0:
		return nil, tt.NewFail(tt.CodeTargetValueUnrepresentable, fmt.Sprintf(
			"%s cannot be a CSV delimiter: it is the quote, a line break or NUL",
			strconv.QuoteRune(options.Delimiter)))
	}
	return &CSVRenderer[O]{
		out:       out,
		options:   options,
		delimiter: string(options.Delimiter),
	}, nil
}

// Options is the dialect.
func (r *CSVRenderer[O]) Options() CSVOptions { return r.options }

// Rows is the rows written so far.
func (r *CSVRenderer[O]) Rows() uint64 { return r.rows }

// IsDone reports whether End has been rendered and flushed.
func (r *CSVRenderer[O]) IsDone() bool { return r.phase == done }

// Inner is the output.
func (r *CSVRenderer[O]) Inner() O { return r.out }

// fail marks a failure as leaving partial output when text this renderer
// wrote has reached the destination; text still buffered in the output
// has not, and the output knows which.
func (r *CSVRenderer[O]) fail(f *tt.Fail) *tt.Fail {
	if r.emitted && r.out.HasCommitted() {
		f.Committed()
	}
	return f
}

func (r *CSVRenderer[O]) schema(columns []tt.PublicColumn) *tt.Fail {
	switch r.phase {
	case inRows:
		return r.fail(tt.ProtocolFail("a second schema"))
	case done:
		return r.fail(tt.ProtocolFail("a schema after the end"))
	}
	if len(columns) == 0 {
		return tt.NewFail(tt.CodeTargetValueUnrepresentable, "a table with no columns has no CSV form")
	}
	r.labels = make([]string, len(columns))
	for i, c := range columns {
		r.labels[i] = c.Label
	}
	r.phase = inRows
	if r.options.Header {
		r.emitted = true
		for i, label := range r.labels {
			if i > 0 {
				if f := r.out.WriteStr(r.delimiter); f != nil {
					return f
				}
			}
			if f := writeField(r.out, r.options.Quoting, r.options.Delimiter, label); f != nil {
				return f
			}
		}
		if f := r.out.WriteStr(r.options.Newline.String()); f != nil {
			return f
		}
	}
	return nil
}

func (r *CSVRenderer[O]) row(cells []tt.Cell) *tt.Fail {
	switch r.phase {
	case beforeSchema:
		return tt.ProtocolFail("a row before the schema")
	case done:
		return r.fail(tt.ProtocolFail("a row after the end"))
	}
	if len(cells) != len(r.labels) {
		return r.fail(tt.ProtocolFail(fmt.Sprintf("row %d has %d cells; the schema has %d columns",
			r.rows+1, len(cells), len(r.labels))))
	}
	if f := r.check(cells); f != nil {
		return r.fail(f)
	}
	for i := range cells {
		cell := &cells[i]
		if i > 0 {
			if f := r.out.WriteStr(r.delimiter); f != nil {
				return f
			}
		}
		var text string
		switch cell.Kind {
		case tt.CellNull:
			text = r.options.NullText
		case tt.CellBool:
			if cell.Bool {
				text = "true"
			} else {
				text = "false"
			}
		case tt.CellNumber:
			// check passed the row: the lexeme is a JSON number and the
			// value is finite, so this pass only formats, once.
			if cell.Lexeme != "" {
				text = cell.Lexeme
			} else {
				r.scratch = appendValue(r.scratch[:0], cell.Value)
				text = string(r.scratch)
			}
		case tt.CellString:
			text = cell.Text
		case tt.CellMissing:
			if r.options.Missing == nil {
				// check rejected this row already.
				continue
			}
			text = *r.options.Missing
		}
		r.emitted = true
		if f := writeField(r.out, r.options.Quoting, r.options.Delimiter, text); f != nil {
			return f
		}
	}
	r.emitted = true
	if f := r.out.WriteStr(r.options.Newline.String()); f != nil {
		return f
	}
	r.rows++
	return nil
}

// check rejects a row before any of it is written, so a row is rendered
// whole or not at all and the output stays a sequence of complete records
// whatever the caller does after a failure. Nothing is formatted here.
func (r *CSVRenderer[O]) check(cells []tt.Cell) *tt.Fail {
	for i := range cells {
		cell := &cells[i]
		switch cell.Kind {
		case tt.CellNumber:
			if f := checkNumber(cell.Value, cell.Lexeme); f != nil {
				return f.AtPath(fmt.Sprintf("column %s, row %d", strconv.Quote(r.labels[i]), r.rows+1))
			}
		case tt.CellMissing:
			if r.options.Missing == nil {
				return tt.NewFail(tt.CodeMissingValue, fmt.Sprintf("row %d has no value for column %s",
					r.rows+1, strconv.Quote(r.labels[i])))
			}
		}
	}
	return nil
}

func (r *CSVRenderer[O]) end() *tt.Fail {
	switch r.phase {
	case beforeSchema:
		return tt.ProtocolFail("the end before the schema")
	case done:
		return r.fail(tt.ProtocolFail("a second end"))
	}
	// Done only once the flush has succeeded: a table whose last bytes
	// never reached the writer is not done, whatever End said.
	if f := r.out.Flush(); f != nil {
		return f
	}
	r.phase = done
	return nil
}

// TableEvent renders one TableRows/1 event.
func (r *CSVRenderer[O]) TableEvent(ev tt.TableEvent) (tt.Flow, *tt.Fail) {
	var f *tt.Fail
	switch ev.Kind {
	case tt.TableSchema:
		f = r.schema(ev.Columns)
	case tt.TableRow:
		f = r.row(ev.Cells)
	case tt.TableEnd:
		f = r.end()
	default:
		f = r.fail(tt.ProtocolFail(fmt.Sprintf("an unknown table event kind %d", ev.Kind)))
	}
	return tt.Continue, f
}

// writeField writes one field: quoted with `"` doubled, or bare when the
// dialect allows and the text needs no quoting.
func writeField(out TextOut, quoting Quoting, delimiter rune, text string) *tt.Fail {
	quote := quoting == QuotingAlways ||
		strings.ContainsRune(text, delimiter) || strings.ContainsAny(text, "\"\r\n")
	if !quote {
		return out.WriteStr(text)
	}
	if f := out.WriteStr(`"`); f != nil {
		return f
	}
	rest := text
	for {
		i := strings.IndexByte(rest, '"')
		if i < 0 {
			break
		}
		// Up to and including the quote, then the quote again: doubled.
		if f := out.WriteStr(rest[:i+1]); f != nil {
			return f
		}
		if f := out.WriteStr(`"`); f != nil {
			return f
		}
		rest = rest[i+1:]
	}
	if rest != "" {
		if f := out.WriteStr(rest); f != nil {
			return f
		}
	}
	return out.WriteStr(`"`)
}
