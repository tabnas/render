// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// Every Appendix A case of rs/src/csv.rs, asserting exact bytes, and the
// protocol errors and committed-output rules beside them.

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	tt "github.com/tabnas/transduce/go"
)

func cols(labels ...string) []tt.PublicColumn {
	out := make([]tt.PublicColumn, len(labels))
	for i, l := range labels {
		out[i] = tt.PublicColumn{Label: l}
	}
	return out
}

func s(text string) tt.Cell { return tt.Cell{Kind: tt.CellString, Text: text} }

func num(lexeme string) tt.Cell {
	v, err := strconv.ParseFloat(lexeme, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		v = 0
	}
	return tt.Cell{Kind: tt.CellNumber, Value: v, Lexeme: lexeme}
}

func val(v float64) tt.Cell { return tt.Cell{Kind: tt.CellNumber, Value: v} }

var (
	null    = tt.Cell{Kind: tt.CellNull}
	missing = tt.Cell{Kind: tt.CellMissing}
)

func boolean(b bool) tt.Cell { return tt.Cell{Kind: tt.CellBool, Bool: b} }

func schemaEv(labels ...string) tt.TableEvent {
	return tt.TableEvent{Kind: tt.TableSchema, Columns: cols(labels...)}
}

func rowEv(cells ...tt.Cell) tt.TableEvent { return tt.TableEvent{Kind: tt.TableRow, Cells: cells} }

var endEv = tt.TableEvent{Kind: tt.TableEnd}

func tableEv(t *testing.T, sink tt.TableSink, ev tt.TableEvent) *tt.Fail {
	t.Helper()
	_, f := sink.TableEvent(ev)
	return f
}

// renderCSV renders a whole table and gives the text back.
func renderCSV(options CSVOptions, labels []string, rows ...[]tt.Cell) (string, *tt.Fail) {
	r, f := NewCSVRenderer(NewStringOut(), options)
	if f != nil {
		return "", f
	}
	if _, f := r.TableEvent(schemaEv(labels...)); f != nil {
		return "", f
	}
	for _, row := range rows {
		if _, f := r.TableEvent(rowEv(row...)); f != nil {
			return "", f
		}
	}
	if _, f := r.TableEvent(endEv); f != nil {
		return "", f
	}
	return r.Inner().String(), nil
}

func standard(t *testing.T, labels []string, rows ...[]tt.Cell) string {
	t.Helper()
	out, f := renderCSV(DefaultCSVOptions(), labels, rows...)
	ok(t, f)
	return out
}

func L(labels ...string) []string { return labels }

func R(cells ...tt.Cell) []tt.Cell { return cells }

func TestEveryFieldIsQuotedAndEveryRecordEndsWithCRLF(t *testing.T) {
	eq(t, standard(t, L("name", "age"), R(s("ada"), num("36"))), "\"name\",\"age\"\r\n\"ada\",\"36\"\r\n", "csv")
}

func TestEmptyFieldsAreAnEmptyQuotePair(t *testing.T) {
	eq(t, standard(t, L("a", "b"), R(s(""), s(""))), "\"a\",\"b\"\r\n\"\",\"\"\r\n", "csv")
}

func TestCommasInAFieldAreKeptInsideTheQuotes(t *testing.T) {
	eq(t, standard(t, L("a"), R(s("x, y, z"))), "\"a\"\r\n\"x, y, z\"\r\n", "csv")
}

func TestQuotesInAFieldAreDoubled(t *testing.T) {
	eq(t, standard(t, L("a"), R(s(`say "hi"`)), R(s(`"`)), R(s(`""`))),
		"\"a\"\r\n\"say \"\"hi\"\"\"\r\n\"\"\"\"\r\n\"\"\"\"\"\"\r\n", "csv")
}

func TestCRAndLFInsideAFieldAreWrittenAsTheyAre(t *testing.T) {
	eq(t, standard(t, L("a"), R(s("line1\r\nline2\nline3\rend"))), "\"a\"\r\n\"line1\r\nline2\nline3\rend\"\r\n", "csv")
}

func TestUnicodePassesThroughUnchanged(t *testing.T) {
	eq(t, standard(t, L("名"), R(s("héllo 日本語 🚀"))), "\"名\"\r\n\"héllo 日本語 🚀\"\r\n", "csv")
}

func TestBooleansWriteTrueAndFalse(t *testing.T) {
	eq(t, standard(t, L("a", "b"), R(boolean(false), boolean(true))), "\"a\",\"b\"\r\n\"false\",\"true\"\r\n", "csv")
}

func TestZeroAndOtherValuesWithoutALexemeTakeTheShortestForm(t *testing.T) {
	eq(t, standard(t, L("z", "b", "big", "bigger", "tiny"), R(val(0), val(50.25), val(1e20), val(1e21), val(1e-300))),
		"\"z\",\"b\",\"big\",\"bigger\",\"tiny\"\r\n\"0\",\"50.25\",\"100000000000000000000\",\"1e21\",\"1e-300\"\r\n", "csv")
}

func TestBigLexemesAreWrittenVerbatim(t *testing.T) {
	eq(t, standard(t, L("a", "b", "c", "d"), R(num("123456789012345678901234567890"),
		num("0.1000000000000000055511151231257827"), num("-1.5E+308"), num("50.250"))),
		"\"a\",\"b\",\"c\",\"d\"\r\n\"123456789012345678901234567890\",\"0.1000000000000000055511151231257827\",\"-1.5E+308\",\"50.250\"\r\n", "csv")
}

func TestCSVALexemeThatIsNotAJSONNumberIsInvalidNumber(t *testing.T) {
	// Rust's list also has "": an empty lexeme is no lexeme in
	// transduce's Go types (see DIVERGENCE.md), so it is not here.
	for _, bad := range []string{"1.", "01", "NaN", "0x10", "1_000"} {
		_, f := renderCSV(DefaultCSVOptions(), L("a"), R(num(bad)))
		code(t, f, tt.CodeInvalidNumber)
		eq(t, f.CommittedOutput, true, "the header was already written: "+bad)
	}
}

func TestNaNAndInfinityAreUnrepresentableWithOrWithoutALexeme(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, f := renderCSV(DefaultCSVOptions(), L("a"), R(val(v)))
		code(t, f, tt.CodeTargetValueUnrepresentable)
	}
	// The lexeme spells a number, but the value beside it overflowed: the
	// row is refused whole and nothing of it is written.
	r, f := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	ok(t, f)
	ok(t, tableEv(t, r, schemaEv("a", "b")))
	f = code(t, tableEv(t, r, rowEv(s("x"), tt.Cell{Kind: tt.CellNumber, Value: math.Inf(1), Lexeme: "1e999"})),
		tt.CodeTargetValueUnrepresentable)
	eq(t, f.Path, `column "b", row 1`, "path")
	eq(t, r.Inner().String(), "\"a\",\"b\"\r\n", "text")
}

func TestNullWritesTheNullTextEmptyByDefault(t *testing.T) {
	eq(t, standard(t, L("a", "b"), R(null, s("x"))), "\"a\",\"b\"\r\n\"\",\"x\"\r\n", "csv")
	options := DefaultCSVOptions()
	options.NullText = "NULL"
	out, f := renderCSV(options, L("a"), R(null))
	ok(t, f)
	eq(t, out, "\"a\"\r\n\"NULL\"\r\n", "csv")
}

func TestMissingIsAnErrorUnlessATextIsConfigured(t *testing.T) {
	_, f := renderCSV(DefaultCSVOptions(), L("a", "b"), R(s("x"), missing))
	code(t, f, tt.CodeMissingValue)
	eq(t, strings.Contains(f.Message, `"b"`), true, "message names the column")
	eq(t, f.CommittedOutput, true, "committed")
	options := DefaultCSVOptions()
	options.Missing = MissingAs("N/A")
	out, f := renderCSV(options, L("a", "b"), R(missing, s("x")))
	ok(t, f)
	eq(t, out, "\"a\",\"b\"\r\n\"N/A\",\"x\"\r\n", "csv")
	options.Missing = MissingAs("")
	out, f = renderCSV(options, L("a"), R(missing))
	ok(t, f)
	eq(t, out, "\"a\"\r\n\"\"\r\n", "csv")
}

func TestARowThatFailsWritesNothingSoRecordsStayWhole(t *testing.T) {
	r, f := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	ok(t, f)
	ok(t, tableEv(t, r, schemaEv("a", "b")))
	for _, c := range []struct {
		row  []tt.Cell
		code tt.Code
	}{
		{R(s("x"), missing), tt.CodeMissingValue},
		{R(s("x"), num("1.")), tt.CodeInvalidNumber},
		{R(s("x"), val(math.NaN())), tt.CodeTargetValueUnrepresentable},
	} {
		code(t, tableEv(t, r, rowEv(c.row...)), c.code)
		eq(t, r.Inner().String(), "\"a\",\"b\"\r\n", c.code.String())
	}
	ok(t, tableEv(t, r, rowEv(s("y"), s("z"))))
	ok(t, tableEv(t, r, endEv))
	eq(t, r.Rows(), 1, "rows")
	eq(t, r.Inner().String(), "\"a\",\"b\"\r\n\"y\",\"z\"\r\n", "text")
}

func TestDuplicateLabelsAreAllowed(t *testing.T) {
	eq(t, standard(t, L("a", "a"), R(s("1"), s("2"))), "\"a\",\"a\"\r\n\"1\",\"2\"\r\n", "csv")
}

func TestAnEmptyRowSequenceStillWritesTheHeader(t *testing.T) {
	eq(t, standard(t, L("a", "b")), "\"a\",\"b\"\r\n", "csv")
}

func TestTheHeaderCanBeTurnedOff(t *testing.T) {
	options := DefaultCSVOptions()
	options.Header = false
	out, f := renderCSV(options, L("a"), R(s("x")))
	ok(t, f)
	eq(t, out, "\"x\"\r\n", "csv")
	out, f = renderCSV(options, L("a"))
	ok(t, f)
	eq(t, out, "", "csv")
}

func TestTheFinalRecordEndsWithTheNewlineToo(t *testing.T) {
	out := standard(t, L("a"), R(s("1")), R(s("2")))
	eq(t, strings.HasSuffix(out, "\"2\"\r\n"), true, "suffix")
	eq(t, strings.Count(out, "\r\n"), 3, "records")
}

func TestLFIsADialect(t *testing.T) {
	options := DefaultCSVOptions()
	options.Newline = NewlineLF
	out, f := renderCSV(options, L("a"), R(s("x")))
	ok(t, f)
	eq(t, out, "\"a\"\n\"x\"\n", "csv")
}

func TestATabDelimiterIsADialect(t *testing.T) {
	options := DefaultCSVOptions()
	options.Delimiter = '\t'
	out, f := renderCSV(options, L("a", "b"), R(s("x,y"), s("z")))
	ok(t, f)
	eq(t, out, "\"a\"\t\"b\"\r\n\"x,y\"\t\"z\"\r\n", "csv")
}

func TestMinimalQuotingQuotesOnlyWhatNeedsIt(t *testing.T) {
	options := DefaultCSVOptions()
	options.Quoting = QuotingMinimal
	out, f := renderCSV(options, L("p", "e", "c", "q", "lf", "cr", "n", "nul"),
		R(s("plain"), s(""), s("a,b"), s(`say "hi"`), s("x\ny"), s("x\ry"), num("1.50"), null))
	ok(t, f)
	eq(t, out, "p,e,c,q,lf,cr,n,nul\r\nplain,,\"a,b\",\"say \"\"hi\"\"\",\"x\ny\",\"x\ry\",1.50,\r\n", "csv")
}

func TestMinimalQuotingQuotesAFieldHoldingTheDialectsDelimiter(t *testing.T) {
	options := DefaultCSVOptions()
	options.Quoting = QuotingMinimal
	options.Delimiter = ';'
	out, f := renderCSV(options, L("a", "b"), R(s("x;y"), s("x,y")))
	ok(t, f)
	eq(t, out, "a;b\r\n\"x;y\";x,y\r\n", "csv")
}

func TestTheQuoteALineBreakAndNULCannotBeTheDelimiter(t *testing.T) {
	for _, bad := range []rune{'"', '\r', '\n', 0} {
		options := DefaultCSVOptions()
		options.Delimiter = bad
		_, f := NewCSVRenderer(NewStringOut(), options)
		code(t, f, tt.CodeTargetValueUnrepresentable)
	}
	for _, good := range []rune{',', ';', '\t', '|', ' ', 'x', '→'} {
		options := DefaultCSVOptions()
		options.Delimiter = good
		_, f := NewCSVRenderer(NewStringOut(), options)
		ok(t, f)
	}
	// The zero value's delimiter is NUL: CSVOptions{} is refused, not
	// silently a dialect.
	_, f := NewCSVRenderer(NewStringOut(), CSVOptions{})
	code(t, f, tt.CodeTargetValueUnrepresentable)
}

func TestZeroColumnsIsUnrepresentable(t *testing.T) {
	_, f := renderCSV(DefaultCSVOptions(), L())
	code(t, f, tt.CodeTargetValueUnrepresentable)
	eq(t, f.CommittedOutput, false, "committed")
}

func TestARowBeforeTheSchemaIsAProtocolError(t *testing.T) {
	r, _ := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	f := code(t, tableEv(t, r, rowEv(s("x"))), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, false, "committed")
}

func TestASecondSchemaIsAProtocolError(t *testing.T) {
	r, _ := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	f := code(t, tableEv(t, r, schemaEv("a")), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, true, "committed")
}

func TestARowOfTheWrongWidthIsAProtocolError(t *testing.T) {
	for _, row := range [][]tt.Cell{R(), R(s("1")), R(s("1"), s("2"), s("3"))} {
		_, f := renderCSV(DefaultCSVOptions(), L("a", "b"), row)
		code(t, f, tt.CodeProtocolOrderError)
		eq(t, strings.Contains(f.Message, "row 1 has"), true, f.Message)
	}
}

func TestAnEndBeforeTheSchemaIsAProtocolError(t *testing.T) {
	r, _ := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	code(t, tableEv(t, r, endEv), tt.CodeProtocolOrderError)
}

func TestEventsAfterTheEndAreProtocolErrors(t *testing.T) {
	r, _ := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	ok(t, tableEv(t, r, endEv))
	eq(t, r.IsDone(), true, "done")
	for _, ev := range []tt.TableEvent{endEv, rowEv(s("x")), schemaEv("a")} {
		f := code(t, tableEv(t, r, ev), tt.CodeProtocolOrderError)
		eq(t, f.CommittedOutput, true, "committed")
	}
}

// noFlush is a writer that takes every byte and refuses to flush.
type noFlush struct{}

func (noFlush) Write(p []byte) (int, error) { return len(p), nil }
func (noFlush) Flush() error                { return errors.New("pipe closed") }

func TestCSVAFailedFlushAtEndLeavesTheRendererNotDone(t *testing.T) {
	r, _ := NewCSVRenderer(NewWriteOut(noFlush{}), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	code(t, tableEv(t, r, endEv), tt.CodeOutputFailed)
	eq(t, r.IsDone(), false, "done")
}

func TestCSVCommittedOutputMeansBytesThatReachedTheWriter(t *testing.T) {
	// Buffered in the WriteOut, not yet written: the failure leaves no
	// partial output behind, and says so.
	var b bytes.Buffer
	r, _ := NewCSVRenderer(NewWriteOut(&b), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	f := code(t, tableEv(t, r, schemaEv("a")), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, false, "committed")
	eq(t, r.Inner().Committed(), 0, "committed bytes")
	eq(t, b.Len(), 0, "written")

	// Written through: partial output exists.
	b.Reset()
	r, _ = NewCSVRenderer(NewWriteOut(&b).WithBudget(0), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	f = code(t, tableEv(t, r, schemaEv("a")), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, true, "committed")
	eq(t, b.String(), "\"a\"\r\n", "written")
}

func TestCSVEndFlushesTheOutputAndNothingElseDoes(t *testing.T) {
	var b bytes.Buffer
	r, _ := NewCSVRenderer(NewWriteOut(&b), DefaultCSVOptions())
	ok(t, tableEv(t, r, schemaEv("a")))
	ok(t, tableEv(t, r, rowEv(s("x"))))
	eq(t, r.Inner().Committed(), 0, "committed before end")
	flow, f := r.TableEvent(endEv)
	ok(t, f)
	eq(t, flow, tt.Continue, "flow")
	eq(t, r.Rows(), 1, "rows")
	eq(t, r.Inner().Committed(), 10, "committed after end")
	eq(t, b.String(), "\"a\"\r\n\"x\"\r\n", "written")
}
