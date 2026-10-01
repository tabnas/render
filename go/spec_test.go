// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// The shared fixtures in ../test/spec, run through tabnas-support's
// Runner as every tabnas repository runs its fixtures. The encodings are
// ../docs/reference.md's "Shared fixtures": the input cells are JSON read
// RAW (no escape codec), the expected cell is the exact output through
// the escape codec (text.tsv's is a JSON array of chunks), and
// ERROR:<CODE> is the first failure's code. A fixture cell that does not
// follow the encodings fails the row loudly as malformed; it never
// becomes a code a row could match.
//
// Every row runs, except the rows specDivergences lists (none today), each
// skipped by name with its reason: a measured difference between this
// runtime and Rust, recorded for DIVERGENCE.md.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	support "github.com/tabnas/support/go"
	tt "github.com/tabnas/transduce/go"
)

// specFixtures is every fixture file and so every runner: a new file
// without a runner fails TestEveryFixtureHasARunner rather than passing
// unread.
var specFixtures = []string{"csv.tsv", "json.tsv", "number.tsv", "records.tsv", "text.tsv"}

// specDivergences is the rows this runtime does not reproduce, by file and
// then by the input column as written in the fixture, and why. There are
// none: every row of every fixture runs.
var specDivergences = map[string]map[string]string{}

// rowFailure is a failure as the shared runner sees it: the code is the
// contract, the whole failure goes in the report.
type rowFailure struct{ fail *tt.Fail }

func (r *rowFailure) Error() string { return r.fail.Error() }
func (r *rowFailure) Code() string  { return r.fail.Code.String() }

// malformed is a defect in the fixture, not a rendering failure.
type malformed struct{ msg string }

func (m *malformed) Error() string { return "malformed fixture cell: " + m.msg }
func (m *malformed) Code() string  { return "MALFORMED_FIXTURE" }

func badCell(format string, args ...any) { panic(&malformed{fmt.Sprintf(format, args...)}) }

type specCount struct{ rows, passed, skipped int }

func specDir(t testing.TB) string {
	t.Helper()
	dir, err := support.FindSpecDir("")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// runSpec runs every row of file. stage renders one row: a value, or the
// failure's code through rowFailure. textExpected reads the expected cell
// as exact text through the escape codec.
func runSpec(t *testing.T, file, inputCol string, textExpected bool, stage func(row *support.Row) (any, *tt.Fail)) {
	spec, err := support.LoadSpec(filepath.Join(specDir(t), file), nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := support.Runner{
		ParseRow: func(_ string, row *support.Row) (got any, err error) {
			defer func() {
				if p := recover(); p != nil {
					m, ok := p.(*malformed)
					if !ok {
						panic(p)
					}
					got, err = nil, m
				}
			}()
			v, f := stage(row)
			if f != nil {
				return nil, &rowFailure{f}
			}
			return v, nil
		},
	}
	if textExpected {
		runner.ParseExpected = func(cell string, _ *support.Row) (any, error) {
			return support.Unescape(cell), nil
		}
	}
	var count specCount
	t.Run("spec: "+file, func(t *testing.T) {
		for _, row := range spec.Rows {
			input := row.Named(inputCol)
			count.rows++
			t.Run(fmt.Sprintf("row %d: %s", row.Line, input), func(t *testing.T) {
				if why, ok := specDivergences[file][input]; ok {
					count.skipped++
					t.Skipf("%s: a runtime divergence: %s", row.Where(), why)
				}
				if err := runner.CheckRow(row, input, row.Named("expected")); err != nil {
					t.Error(err)
					return
				}
				count.passed++
			})
		}
	})
	// Every key in specDivergences must name a row that exists, or a
	// repaired fixture would leave a stale skip behind.
	for input := range specDivergences[file] {
		found := false
		for _, row := range spec.Rows {
			if row.Named(inputCol) == input {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the divergence %q names no row", file, input)
		}
	}
	t.Logf("%s: %d rows, %d passed, %d skipped", file, count.rows, count.passed, count.skipped)
	fmt.Printf("spec %s: %d rows, %d passed, %d skipped\n", file, count.rows, count.passed, count.skipped)
}

// TestEveryFixtureHasARunner fails when a fixture appears without a
// runner, rather than letting it pass unread.
func TestEveryFixtureHasARunner(t *testing.T) {
	specs, err := support.LoadSpecDir(specDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, s := range specs {
		files = append(files, s.Name)
	}
	sort.Strings(files)
	if fmt.Sprint(files) != fmt.Sprint(specFixtures) {
		t.Fatalf("fixtures %v, runners %v: each fixture needs a TestSpec runner", files, specFixtures)
	}
}

// The cell decoders.

// jsonColumn decodes a JSON column, read RAW, numbers kept as
// json.Number; an empty cell is def.
func jsonColumn(row *support.Row, name, def string) any {
	cell := row.Named(name)
	if cell == "" {
		cell = def
	}
	dec := json.NewDecoder(strings.NewReader(cell))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		badCell("%s: %v", name, err)
	}
	if dec.More() {
		badCell("%s: trailing text", name)
	}
	return v
}

// object is a JSON options object, every field of it one of known: a
// misspelt option would otherwise be ignored and the row would test the
// default.
func object(v any, what string, known ...string) map[string]any {
	o, ok := v.(map[string]any)
	if !ok {
		badCell("%s %v is not an object", what, v)
	}
	for k := range o {
		ok := false
		for _, n := range known {
			ok = ok || n == k
		}
		if !ok {
			badCell("unknown %s field %q", what, k)
		}
	}
	return o
}

func uintField(v any, what string) uint64 {
	n, ok := v.(json.Number)
	if !ok {
		badCell("%s %v is not a count", what, v)
	}
	u, err := strconv.ParseUint(n.String(), 10, 64)
	if err != nil {
		badCell("%s %v is not a count", what, v)
	}
	return u
}

func text(v any) string {
	s, ok := v.(string)
	if !ok {
		badCell("%v is not a string", v)
	}
	return s
}

// parseValue is a value spelled as fixture text: a JSON number finite as
// a float64, or NaN, Infinity or -Infinity.
func parseValue(s string) float64 {
	switch s {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if IsJSONNumber(s) {
		if v, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(v, 0) {
			return v
		}
	}
	badCell("%q is not a value", s)
	return 0
}

// number is a number object: {"num": lexeme}, {"value": value}, or both;
// the bool says whether it has a lexeme, so {"num": ""} is the empty
// lexeme, not none. With a lexeme and no value, the value is the lexeme
// read as a decimal when it is a JSON number (overflowing to infinity, as
// 1e999 does) and 0 when it is not.
func number(o map[string]any) (float64, string, bool) {
	object(o, "number", "num", "value")
	field := func(k string) (string, bool) {
		v, ok := o[k]
		if !ok {
			return "", false
		}
		return text(v), true
	}
	lexeme, hasLexeme := field("num")
	if v, ok := field("value"); ok {
		return parseValue(v), lexeme, hasLexeme
	}
	if !hasLexeme {
		badCell("a number object names num, value or both")
	}
	if IsJSONNumber(lexeme) {
		v, err := strconv.ParseFloat(lexeme, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			badCell("%q: %v", lexeme, err)
		}
		return v, lexeme, true
	}
	return 0, lexeme, true
}

// cell is one TableRows/1 cell: null, true, false, a string, a number
// object or {"missing": true}. A bare JSON number is refused: it cannot
// say whether it carries a lexeme.
func cell(v any) tt.Cell {
	switch c := v.(type) {
	case nil:
		return tt.Cell{Kind: tt.CellNull}
	case bool:
		return tt.Cell{Kind: tt.CellBool, Bool: c}
	case string:
		return tt.Cell{Kind: tt.CellString, Text: c}
	case map[string]any:
		if m, ok := c["missing"]; ok && len(c) == 1 && m == true {
			return tt.Cell{Kind: tt.CellMissing}
		}
		value, lexeme, hasLexeme := number(c)
		return tt.Cell{Kind: tt.CellNumber, HasLexeme: hasLexeme, Value: value, Lexeme: lexeme}
	}
	badCell("%v is not a cell", v)
	return tt.Cell{}
}

// tableEvents is a table fixture's events column.
func tableEvents(v any) []tt.TableEvent {
	items, ok := v.([]any)
	if !ok {
		badCell("events is a JSON array")
	}
	var out []tt.TableEvent
	for _, item := range items {
		switch it := item.(type) {
		case string:
			if it != "end" {
				badCell("%q is not a table event", it)
			}
			out = append(out, tt.TableEvent{Kind: tt.TableEnd})
			continue
		case map[string]any:
			if len(it) == 1 {
				if labels, ok := it["schema"].([]any); ok {
					cols := make([]tt.PublicColumn, 0, len(labels))
					for _, l := range labels {
						cols = append(cols, tt.PublicColumn{Label: text(l)})
					}
					out = append(out, tt.TableEvent{Kind: tt.TableSchema, Columns: cols})
					continue
				}
				if cells, ok := it["row"].([]any); ok {
					row := make([]tt.Cell, 0, len(cells))
					for _, c := range cells {
						row = append(row, cell(c))
					}
					out = append(out, tt.TableEvent{Kind: tt.TableRow, Cells: row})
					continue
				}
			}
		}
		badCell("%v is not a table event", item)
	}
	return out
}

// jsonEvents is json.tsv's events column.
func jsonEvents(v any) []tt.Event {
	items, ok := v.([]any)
	if !ok {
		badCell("events is a JSON array")
	}
	var out []tt.Event
	for _, item := range items {
		switch it := item.(type) {
		case string:
			switch it {
			case "{":
				out = append(out, tt.EvObjectStart())
			case "}":
				out = append(out, tt.EvObjectEnd())
			case "[":
				out = append(out, tt.EvArrayStart())
			case "]":
				out = append(out, tt.EvArrayEnd())
			case "end":
				out = append(out, tt.EvEnd())
			default:
				badCell("%q is not a JSON event", it)
			}
		case nil:
			out = append(out, tt.EvNull())
		case bool:
			out = append(out, tt.EvBool(it))
		case map[string]any:
			if k, ok := it["key"]; ok && len(it) == 1 {
				out = append(out, tt.EvKey(text(k)))
			} else if s, ok := it["str"]; ok && len(it) == 1 {
				out = append(out, tt.EvString(text(s)))
			} else {
				value, lexeme, hasLexeme := number(it)
				out = append(out, tt.Event{Kind: tt.Number, HasLexeme: hasLexeme, Value: value, Lexeme: lexeme})
			}
		default:
			badCell("%v is not a JSON event", item)
		}
	}
	return out
}

func feedTable(sink tt.TableSink, events []tt.TableEvent) *tt.Fail {
	for _, ev := range events {
		if _, f := sink.TableEvent(ev); f != nil {
			return f
		}
	}
	return nil
}

func feedJSON(sink tt.Sink, events []tt.Event) *tt.Fail {
	for _, ev := range events {
		if _, f := sink.Event(ev); f != nil {
			return f
		}
	}
	return nil
}

// The runners.

func csvOptions(row *support.Row) CSVOptions {
	o := object(jsonColumn(row, "options", "{}"), "options",
		"delimiter", "newline", "header", "null_text", "missing", "quoting")
	options := DefaultCSVOptions()
	if d, ok := o["delimiter"]; ok {
		runes := []rune(text(d))
		if len(runes) != 1 {
			badCell("delimiter %v is not one character", d)
		}
		options.Delimiter = runes[0]
	}
	if n, ok := o["newline"]; ok {
		switch n {
		case "lf":
			options.Newline = NewlineLF
		case "crlf":
			options.Newline = NewlineCRLF
		default:
			badCell("newline %v", n)
		}
	}
	if h, ok := o["header"]; ok {
		b, isBool := h.(bool)
		if !isBool {
			badCell("header %v", h)
		}
		options.Header = b
	}
	if n, ok := o["null_text"]; ok {
		options.NullText = text(n)
	}
	if m, ok := o["missing"]; ok && m != nil {
		options.Missing = MissingAs(text(m))
	}
	if q, ok := o["quoting"]; ok {
		switch q {
		case "always":
			options.Quoting = QuotingAlways
		case "minimal":
			options.Quoting = QuotingMinimal
		default:
			badCell("quoting %v", q)
		}
	}
	return options
}

func TestSpecCSV(t *testing.T) {
	runSpec(t, "csv.tsv", "events", true, func(row *support.Row) (any, *tt.Fail) {
		events := tableEvents(jsonColumn(row, "events", ""))
		r, f := NewCSVRenderer(NewStringOut(), csvOptions(row))
		if f != nil {
			return nil, f
		}
		if f := feedTable(r, events); f != nil {
			return nil, f
		}
		return r.Inner().String(), nil
	})
}

func jsonOptions(row *support.Row) JSONOptions {
	o := object(jsonColumn(row, "options", "{}"), "options", "indent", "trailing_newline")
	var options JSONOptions
	if n, ok := o["indent"]; ok && n != nil {
		options.Indent = int(uintField(n, "indent"))
	}
	if b, ok := o["trailing_newline"]; ok {
		v, isBool := b.(bool)
		if !isBool {
			badCell("trailing_newline %v", b)
		}
		options.TrailingNewline = v
	}
	return options
}

func TestSpecJSON(t *testing.T) {
	runSpec(t, "json.tsv", "events", true, func(row *support.Row) (any, *tt.Fail) {
		events := jsonEvents(jsonColumn(row, "events", ""))
		r := NewJSONRenderer(NewStringOut(), jsonOptions(row))
		if f := feedJSON(r, events); f != nil {
			return nil, f
		}
		return r.Inner().String(), nil
	})
}

func numberThroughJSON(value float64, lexeme string, hasLexeme bool) (string, *tt.Fail) {
	r := NewJSONRenderer(NewStringOut(), JSONOptions{})
	ev := tt.Event{Kind: tt.Number, HasLexeme: hasLexeme, Value: value, Lexeme: lexeme}
	if f := feedJSON(r, []tt.Event{ev, tt.EvEnd()}); f != nil {
		return "", f
	}
	return r.Inner().String(), nil
}

func numberThroughCSV(value float64, lexeme string, hasLexeme bool) (string, *tt.Fail) {
	options := DefaultCSVOptions()
	options.Header = false
	options.Newline = NewlineLF
	options.Quoting = QuotingMinimal
	r, f := NewCSVRenderer(NewStringOut(), options)
	if f != nil {
		return "", f
	}
	if f := feedTable(r, []tt.TableEvent{
		{Kind: tt.TableSchema, Columns: []tt.PublicColumn{{Label: "n"}}},
		{Kind: tt.TableRow, Cells: []tt.Cell{{Kind: tt.CellNumber, HasLexeme: hasLexeme, Value: value, Lexeme: lexeme}}},
		{Kind: tt.TableEnd},
	}); f != nil {
		return "", f
	}
	return strings.TrimSuffix(r.Inner().String(), "\n"), nil
}

// TestSpecNumber runs each number through both renderers, which must
// agree (text, or code) before the row is compared.
func TestSpecNumber(t *testing.T) {
	runSpec(t, "number.tsv", "number", true, func(row *support.Row) (any, *tt.Fail) {
		o, ok := jsonColumn(row, "number", "").(map[string]any)
		if !ok {
			badCell("number is a number object")
		}
		value, lexeme, hasLexeme := number(o)
		a, fa := numberThroughJSON(value, lexeme, hasLexeme)
		b, fb := numberThroughCSV(value, lexeme, hasLexeme)
		switch {
		case fa == nil && fb == nil && a == b:
			return a, nil
		case fa != nil && fb != nil && fa.Code == fb.Code:
			return nil, fa
		}
		t.Errorf("%s: the renderers disagree: JSON %q %v, CSV %q %v", row.Where(), a, fa, b, fb)
		return nil, tt.NewFail(tt.CodeAborted, "the renderers disagree")
	})
}

func TestSpecRecords(t *testing.T) {
	runSpec(t, "records.tsv", "events", true, func(row *support.Row) (any, *tt.Fail) {
		events := tableEvents(jsonColumn(row, "events", ""))
		o := object(jsonColumn(row, "options", "{}"), "options", "missing")
		missing := MissingSkip
		if m, ok := o["missing"]; ok {
			switch m {
			case "skip":
			case "null":
				missing = MissingNull
			case "error":
				missing = MissingError
			default:
				badCell("missing %v", m)
			}
		}
		renderer := NewJSONRenderer(NewStringOut(), JSONOptions{})
		records := NewRecordsToJSON(renderer).WithMissing(missing)
		if f := feedTable(records, events); f != nil {
			return nil, f
		}
		return records.Inner().Inner().String(), nil
	})
}

// chunkRecorder is a writer that keeps each Write call as one chunk and
// takes it whole.
type chunkRecorder struct{ chunks []any }

func (c *chunkRecorder) Write(p []byte) (int, error) {
	c.chunks = append(c.chunks, string(p))
	return len(p), nil
}

// textTop is the outermost stage, kept by kind so the item markers can
// reach it.
type textTop struct {
	out   TextOut
	items interface {
		ItemStart() *tt.Fail
		ItemEnd() *tt.Fail
	}
}

// buildText builds the stack: the writer, then the stages from the
// innermost (the last listed) outwards.
func buildText(row *support.Row, rec *chunkRecorder) textTop {
	pipeline := object(jsonColumn(row, "pipeline", "{}"), "pipeline", "budget", "limit", "stages")
	w := NewWriteOut(rec)
	if b, ok := pipeline["budget"]; ok {
		w.WithBudget(int(uintField(b, "budget")))
	}
	if l, ok := pipeline["limit"]; ok {
		limit := uintField(l, "limit")
		limits := tt.DefaultLimits()
		limits.MaxOutputBytes = &limit
		w.WithLimits(limits)
	}
	top := textTop{out: w}
	var stages []any
	if s, ok := pipeline["stages"]; ok {
		if stages, ok = s.([]any); !ok {
			badCell("stages %v", s)
		}
	}
	for i := len(stages) - 1; i >= 0; i-- {
		stage, ok := stages[i].(map[string]any)
		if !ok || len(stage) != 1 {
			badCell("%v is not a stage", stages[i])
		}
		inner := top.out
		switch {
		case stage["join"] != nil:
			j := NewJoin(inner, text(stage["join"]))
			top = textTop{out: j, items: j}
		case stage["concat"] == true:
			c := NewConcat(inner)
			top = textTop{out: c, items: c}
		case stage["replace"] != nil:
			pair, ok := stage["replace"].([]any)
			if !ok || len(pair) != 2 {
				badCell("%v is not a stage", stage)
			}
			top = textTop{out: NewReplaceText(inner, text(pair[0]), text(pair[1]))}
		default:
			badCell("%v is not a stage", stage)
		}
	}
	return top
}

func TestSpecText(t *testing.T) {
	runSpec(t, "text.tsv", "ops", false, func(row *support.Row) (any, *tt.Fail) {
		rec := &chunkRecorder{chunks: []any{}}
		top := buildText(row, rec)
		ops, ok := jsonColumn(row, "ops", "").([]any)
		if !ok {
			badCell("ops is a JSON array")
		}
		for _, op := range ops {
			var f *tt.Fail
			switch o := op.(type) {
			case string:
				f = top.out.WriteStr(o)
			case map[string]any:
				if len(o) != 1 {
					badCell("%v is not an operation", op)
				}
				switch o["op"] {
				case "start", "end":
					if top.items == nil {
						badCell("item markers need a join or concat outermost")
					}
					if o["op"] == "start" {
						f = top.items.ItemStart()
					} else {
						f = top.items.ItemEnd()
					}
				case "flush":
					f = top.out.Flush()
				default:
					badCell("%v is not an operation", op)
				}
			default:
				badCell("%v is not an operation", op)
			}
			if f != nil {
				return nil, f
			}
		}
		// Nothing is flushed for the row: what is still buffered never
		// reached the writer, and is not in the result.
		return rec.chunks, nil
	})
}
