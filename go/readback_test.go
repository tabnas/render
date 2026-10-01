// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// The rendered text, read back by independent readers: encoding/csv for
// the CSV renderer, encoding/json for the JSON renderer. Exact bytes are
// asserted beside the renderers; these tests ask the other question,
// whether a reader that knows nothing of this package gets the cells (or
// the document) back. A disagreement here is a defect in the renderer
// whatever the bytes look like.
//
// The JSON documents are rs/tests/fixtures/ (copied from aless), parsed
// by the Go grammars through transduce's ParserSource and rendered; the
// result must equal, as encoding/json reads it, the same events built
// into a value by transduce's DatumBuilder and written by its own JSON
// writer, member order included. Numbers are compared as float64 on both
// sides, so 3 and 3.0 are the same number here.

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tabnascsv "github.com/tabnas/csv/go"
	tabnasjson "github.com/tabnas/json/go"
	tabnasjsonl "github.com/tabnas/jsonl/go"
	tabnas "github.com/tabnas/parser/go"
	tt "github.com/tabnas/transduce/go"
	tabnasyaml "github.com/tabnas/yaml/go"
)

// The CSV oracle.

var hazardLabels = L("plain", "empty", "comma", "quote", "breaks", "unicode", "number", "flag")

// hazards is a table whose cells exercise every quoting hazard at once,
// and the strings a reader must get back.
func hazards() ([][]tt.Cell, [][]string) {
	rows := [][]tt.Cell{
		R(s("ada"), s(""), s("x, y"), s(`say "hi"`), s("a\r\nb\nc\rd"), s("héllo 日本語 🚀"),
			tt.Cell{Kind: tt.CellNumber, HasLexeme: true, Value: 50.25, Lexeme: "50.250"}, boolean(true)),
		R(s(`"`), null, s(","), s(`""`), s("\n"), s("→"), val(0), boolean(false)),
		R(missing, s(" "), s(",,"), s(`a"b`), s("\r"), s("ß"),
			tt.Cell{Kind: tt.CellNumber, HasLexeme: true, Value: -1.5e300, Lexeme: "-1.5E+300"}, boolean(true)),
	}
	expected := [][]string{
		{"ada", "", "x, y", `say "hi"`, "a\r\nb\nc\rd", "héllo 日本語 🚀", "50.250", "true"},
		{`"`, "", ",", `""`, "\n", "→", "0", "false"},
		{"?", " ", ",,", `a"b`, "\r", "ß", "-1.5E+300", "true"},
	}
	return rows, expected
}

func renderHazards(t *testing.T, options CSVOptions) (string, [][]string) {
	t.Helper()
	rows, expected := hazards()
	out, f := renderCSV(options, hazardLabels, rows...)
	ok(t, f)
	return out, expected
}

func readBack(t *testing.T, text string, delimiter rune) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delimiter
	r.FieldsPerRecord = 0
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("encoding/csv refused the output: %v\n%s", err, text)
	}
	return records
}

func hazardOptions() CSVOptions {
	o := DefaultCSVOptions()
	o.Missing = MissingAs("?")
	return o
}

func assertReadBack(t *testing.T, text string, delimiter rune, expected [][]string) {
	t.Helper()
	records := readBack(t, text, delimiter)
	eq(t, fmt.Sprintf("%q", records[0]), fmt.Sprintf("%q", hazardLabels), "header")
	eq(t, len(records)-1, len(expected), "records")
	for i, want := range expected {
		// encoding/csv folds a CRLF inside a quoted field to LF, as its
		// documentation says; it keeps a lone CR. That is the reader's
		// normalisation, not the renderer's, so the expectation takes it.
		folded := make([]string, len(want))
		for j, w := range want {
			folded[j] = strings.ReplaceAll(w, "\r\n", "\n")
		}
		eq(t, fmt.Sprintf("%q", records[i+1]), fmt.Sprintf("%q", folded), fmt.Sprintf("record %d", i+1))
	}
}

func TestTheStandardProfileReadsBackCellForCell(t *testing.T) {
	text, expected := renderHazards(t, hazardOptions())
	assertReadBack(t, text, ',', expected)
}

func TestMinimalQuotingReadsBackCellForCell(t *testing.T) {
	o := hazardOptions()
	o.Quoting = QuotingMinimal
	text, expected := renderHazards(t, o)
	assertReadBack(t, text, ',', expected)
}

func TestATabDelimitedLFDialectReadsBackCellForCell(t *testing.T) {
	o := hazardOptions()
	o.Delimiter = '\t'
	o.Newline = NewlineLF
	text, expected := renderHazards(t, o)
	assertReadBack(t, text, '\t', expected)
	o = hazardOptions()
	o.Delimiter = ';'
	o.Newline = NewlineLF
	o.Quoting = QuotingMinimal
	text, expected = renderHazards(t, o)
	assertReadBack(t, text, ';', expected)
}

func TestManyRowsReadBackWithTheRightCount(t *testing.T) {
	r, f := NewCSVRenderer(NewStringOut(), DefaultCSVOptions())
	ok(t, f)
	ok(t, tableEv(t, r, schemaEv("i", "text")))
	for i := 0; i < 1000; i++ {
		ok(t, tableEv(t, r, rowEv(val(float64(i)), s(fmt.Sprintf("row %d, \"quoted\"\n", i)))))
	}
	ok(t, tableEv(t, r, endEv))
	records := readBack(t, r.Inner().String(), ',')
	eq(t, len(records), 1001, "records")
	eq(t, records[1000][0], "999", "number")
	eq(t, records[1000][1], "row 999, \"quoted\"\n", "text")
}

// The JSON oracle.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "rs", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// canon is a JSON text as encoding/json reads it, re-written with members
// in their order and every number as the float64 it reads as. It fails on
// anything that is not exactly one JSON value.
func canon(t *testing.T, text string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var b strings.Builder
	var value func() error
	value = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case json.Delim:
			close := "]"
			if v == '{' {
				close = "}"
			}
			b.WriteString(string(v))
			for i := 0; dec.More(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				if v == '{' {
					k, err := dec.Token()
					if err != nil {
						return err
					}
					b.WriteString(strconv.Quote(k.(string)) + ":")
				}
				if err := value(); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			b.WriteString(close)
		case json.Number:
			f, err := strconv.ParseFloat(v.String(), 64)
			if err != nil || math.IsInf(f, 0) {
				return fmt.Errorf("number %s out of range", v)
			}
			b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		case string:
			b.WriteString(strconv.Quote(v))
		case nil:
			b.WriteString("null")
		case bool:
			b.WriteString(strconv.FormatBool(v))
		}
		return nil
	}
	if err := value(); err != nil {
		t.Fatalf("encoding/json refused the output: %v\n%s", err, text)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing text after the document: %v\n%s", err, text)
	}
	return b.String()
}

// sourceEvents parses text with the parser through transduce's
// ParserSource (materialize mode, so a grammar's record field order is
// kept) and records the events, End included.
func sourceEvents(t *testing.T, parser *tabnas.Tabnas, text string) []tt.Event {
	t.Helper()
	rec := &tt.Recorder{}
	if _, f := tt.NewParserSource(parser, text).Run(rec); f != nil {
		t.Fatal(f)
	}
	return rec.Events
}

// expectedJSON is the document as transduce's own builder and JSON writer
// give it.
func expectedJSON(t *testing.T, events []tt.Event) string {
	t.Helper()
	b := tt.NewDatumBuilder(math.MaxInt, "max_capture_bytes", tt.LastWins)
	for _, ev := range events {
		if ev.Kind == tt.End {
			break
		}
		if f := b.Event(ev); f != nil {
			t.Fatal(f)
		}
	}
	d, done := b.Take()
	if !done {
		t.Fatal("the events are not one whole value")
	}
	var out strings.Builder
	tt.WriteJSON(&d, &out)
	return out.String()
}

func renderEvents(t *testing.T, options JSONOptions, events []tt.Event) string {
	t.Helper()
	r := NewJSONRenderer(NewStringOut(), options)
	if _, f := tt.Replay(events, r); f != nil {
		t.Fatal(f)
	}
	eq(t, r.IsDone(), true, "done")
	return r.Inner().String()
}

func assertRoundTrip(t *testing.T, name string, events []tt.Event) {
	t.Helper()
	want := canon(t, expectedJSON(t, events))
	for _, options := range []JSONOptions{{}, {Indent: 2, TrailingNewline: true}} {
		text := renderEvents(t, options, events)
		eq(t, canon(t, text), want, fmt.Sprintf("%s with %+v", name, options))
	}
	if strings.Contains(renderEvents(t, JSONOptions{}, events), "\n") {
		t.Errorf("%s: compact output has a line break", name)
	}
}

func csvParser(t *testing.T, options ...map[string]any) *tabnas.Tabnas {
	t.Helper()
	j := tabnas.Make()
	if err := j.UseDefaults(tabnascsv.Csv, tabnascsv.Defaults, options...); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJSONFixturesReadBackAsTheParsedDocument(t *testing.T) {
	for _, name := range []string{"sample.json", "nested.json"} {
		assertRoundTrip(t, name, sourceEvents(t, tabnasjson.Make(), fixture(t, name)))
	}
}

func TestTheJSONLFixtureReadsBackAsTheArrayOfItsLines(t *testing.T) {
	events := sourceEvents(t, tabnasjsonl.Make(), fixture(t, "sample.jsonl"))
	assertRoundTrip(t, "sample.jsonl", events)
	var lines []any
	if err := json.Unmarshal([]byte(renderEvents(t, JSONOptions{}, events)), &lines); err != nil || len(lines) != 3 {
		t.Errorf("lines %v (%v)", lines, err)
	}
}

func TestTheYAMLFixtureReadsBackAsTheParsedDocument(t *testing.T) {
	assertRoundTrip(t, "sample.yaml", sourceEvents(t, tabnasyaml.MakeJsonic(), fixture(t, "sample.yaml")))
}

func TestTheCSVFixtureReadsBackAsTheParsedRecords(t *testing.T) {
	assertRoundTrip(t, "sample.csv", sourceEvents(t, csvParser(t), fixture(t, "sample.csv")))
}

func TestTheCompactRenderingOfTheJSONFixtureIsTheExpectedBytes(t *testing.T) {
	eq(t, renderEvents(t, JSONOptions{}, sourceEvents(t, tabnasjson.Make(), fixture(t, "sample.json"))),
		`{"store":{"name":"corner shop","open":true,"books":[{"title":"SICP","price":42.5,"tags":["cs","classic"]},{"title":"TAPL","price":55,"tags":["types"]}],"counts":{"fiction":12,"science":7}},"version":3}`,
		"json")
}

func TestTheTSVFixtureReadsBackThroughTheCSVGrammarsTabDialect(t *testing.T) {
	parser := csvParser(t, map[string]any{"field": map[string]any{"separation": "\t"}})
	events := sourceEvents(t, parser, fixture(t, "sample.tsv"))
	assertRoundTrip(t, "sample.tsv", events)
	eq(t, renderEvents(t, JSONOptions{}, events), `[{"name":"ada","age":"36"},{"name":"lin","age":"28"}]`, "json")
}
