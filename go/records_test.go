// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	tt "github.com/tabnas/transduce/go"
)

// show is a recording in the Rust recording's short form, one event per
// space.
func show(events []tt.Event) string {
	parts := make([]string, len(events))
	for i, ev := range events {
		parts[i] = ev.String()
		if ev.Kind == tt.Number && !ev.HasLexeme {
			parts[i] += "~" // no lexeme
		}
	}
	return strings.Join(parts, " ")
}

func runRecords(t *testing.T, missing MissingRecord, labels []string, rows ...[]tt.Cell) (string, *tt.Fail) {
	t.Helper()
	rec := &tt.Recorder{}
	r := NewRecordsToJSON(rec).WithMissing(missing)
	for _, ev := range append(append([]tt.TableEvent{schemaEv(labels...)}, rowsOf(rows)...), endEv) {
		if _, f := r.TableEvent(ev); f != nil {
			return "", f
		}
	}
	eq(t, r.IsDone(), true, "done")
	return show(rec.Events), nil
}

func rowsOf(rows [][]tt.Cell) []tt.TableEvent {
	out := make([]tt.TableEvent, len(rows))
	for i, r := range rows {
		out[i] = rowEv(r...)
	}
	return out
}

func records(t *testing.T, missing MissingRecord, labels []string, rows ...[]tt.Cell) string {
	t.Helper()
	out, f := runRecords(t, missing, labels, rows...)
	ok(t, f)
	return out
}

func TestATableBecomesAnArrayOfObjectsKeyedByLabel(t *testing.T) {
	eq(t, records(t, MissingSkip, L("id", "name", "ok"),
		R(tt.Cell{Kind: tt.CellNumber, HasLexeme: true, Value: 1, Lexeme: "1.0"}, s("ada"), boolean(true)),
		R(val(2), null, boolean(false))),
		`[ { key "id" 1.0 key "name" "ada" key "ok" true } { key "id" 2~ key "name" null key "ok" false } ] end`, "events")
}

func TestNoRowsIsAnEmptyArray(t *testing.T) {
	eq(t, records(t, MissingSkip, L("a")), "[ ] end", "events")
}

func TestZeroColumnsGivesEmptyObjects(t *testing.T) {
	eq(t, records(t, MissingSkip, L(), R(), R()), "[ { } { } ] end", "events")
}

func TestAMissingCellIsSkippedByDefault(t *testing.T) {
	eq(t, records(t, MissingSkip, L("a", "b"), R(missing, s("x"))), `[ { key "b" "x" } ] end`, "events")
}

func TestAMissingCellCanBeNullOrAnError(t *testing.T) {
	eq(t, records(t, MissingNull, L("a", "b"), R(missing, s("x"))),
		`[ { key "a" null key "b" "x" } ] end`, "events")
	rec := &tt.Recorder{}
	r := NewRecordsToJSON(rec).WithMissing(MissingError)
	ok(t, tableEv(t, r, schemaEv("a", "b")))
	f := code(t, tableEv(t, r, rowEv(s("x"), missing)), tt.CodeMissingValue)
	eq(t, strings.Contains(f.Message, `"b"`), true, "message")
	eq(t, f.CommittedOutput, true, "the array start was forwarded")
	eq(t, show(rec.Events), "[", "nothing of the row was")
}

func TestARepeatedLabelKeepsTheLastValueThatIsPresent(t *testing.T) {
	rows := [][]tt.Cell{
		R(s("1"), s("2"), s("3")),
		R(s("4"), s("5"), missing),
		R(missing, s("6"), missing),
	}
	eq(t, records(t, MissingSkip, L("a", "b", "a"), rows...),
		`[ { key "b" "2" key "a" "3" } { key "a" "4" key "b" "5" } { key "b" "6" } ] end`, "skip")
	eq(t, records(t, MissingNull, L("a", "b", "a"), rows[1]),
		`[ { key "b" "5" key "a" null } ] end`, "null")
	// Three columns with one label: the middle one wins when the last is
	// absent.
	eq(t, records(t, MissingSkip, L("a", "a", "a"), R(s("1"), s("2"), missing)),
		`[ { key "a" "2" } ] end`, "three")
}

func TestProtocolErrorsMatchTheCSVRenderers(t *testing.T) {
	rec := &tt.Recorder{}
	r := NewRecordsToJSON(rec)
	f := code(t, tableEv(t, r, rowEv(s("x"))), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, false, "committed")
	code(t, tableEv(t, r, endEv), tt.CodeProtocolOrderError)

	ok(t, tableEv(t, r, schemaEv("a")))
	f = code(t, tableEv(t, r, schemaEv("a")), tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, true, "committed")
	f = code(t, tableEv(t, r, rowEv(s("x"), s("y"))), tt.CodeProtocolOrderError)
	eq(t, strings.Contains(f.Message, "row 1 has 2 cells"), true, f.Message)
	code(t, tableEv(t, r, rowEv()), tt.CodeProtocolOrderError)

	ok(t, tableEv(t, r, endEv))
	for _, ev := range []tt.TableEvent{endEv, rowEv(s("x")), schemaEv("a")} {
		f := code(t, tableEv(t, r, ev), tt.CodeProtocolOrderError)
		eq(t, f.CommittedOutput, true, "committed")
	}
	eq(t, r.Rows(), 0, "rows")
	eq(t, show(rec.Events), "[ ] end", "events")
}

func TestASinkThatFailsOnEndLeavesTheStageNotDone(t *testing.T) {
	sink := tt.FnSink(func(ev tt.Event) (tt.Flow, *tt.Fail) {
		if ev.Kind == tt.End {
			return tt.Continue, tt.OutputFail("closed")
		}
		return tt.Continue, nil
	})
	r := NewRecordsToJSON(sink)
	ok(t, tableEv(t, r, schemaEv("a")))
	code(t, tableEv(t, r, endEv), tt.CodeOutputFailed)
	eq(t, r.IsDone(), false, "done")
}

func TestKeysBorrowTheSchemasLabelsRatherThanCopyingThemPerCell(t *testing.T) {
	// Two live allocations cannot share an address, so a key that is a
	// per-cell copy of the label would point elsewhere than the label the
	// stage retains; the same address on every row proves the borrow.
	var seen []*byte
	sink := tt.FnSink(func(ev tt.Event) (tt.Flow, *tt.Fail) {
		if ev.Kind == tt.Key {
			seen = append(seen, unsafe.StringData(ev.Text))
		}
		return tt.Continue, nil
	})
	r := NewRecordsToJSON(sink)
	ok(t, tableEv(t, r, schemaEv("first", "second")))
	ok(t, tableEv(t, r, rowEv(s("x"), null)))
	ok(t, tableEv(t, r, rowEv(s("x"), null)))
	labels := []*byte{unsafe.StringData(r.labels[0]), unsafe.StringData(r.labels[1])}
	eq(t, fmt.Sprint(seen), fmt.Sprint(append(labels, labels...)), "key addresses")
}

func TestAStopFromTheSinkStopsTheRow(t *testing.T) {
	seen := 0
	sink := tt.FnSink(func(tt.Event) (tt.Flow, *tt.Fail) {
		seen++
		if seen == 3 {
			return tt.Stop, nil
		}
		return tt.Continue, nil
	})
	r := NewRecordsToJSON(sink)
	flow, f := r.TableEvent(schemaEv("a", "b"))
	ok(t, f)
	eq(t, flow, tt.Continue, "schema flow")
	flow, f = r.TableEvent(rowEv(s("x"), s("y")))
	ok(t, f)
	eq(t, flow, tt.Stop, "row flow")
	eq(t, seen, 3, "ArrayStart, ObjectStart, the first key, then no more")
}

func TestRecordsRenderAsJSONText(t *testing.T) {
	r := NewRecordsToJSON(NewJSONRenderer(NewStringOut(), JSONOptions{}))
	ok(t, tableEv(t, r, schemaEv("name", "age")))
	ok(t, tableEv(t, r, rowEv(s("ada"), num("36"))))
	ok(t, tableEv(t, r, rowEv(s("lin"), missing)))
	ok(t, tableEv(t, r, endEv))
	eq(t, r.Rows(), 2, "rows")
	eq(t, r.Inner().Inner().String(), `[{"name":"ada","age":36},{"name":"lin"}]`, "json")
}
