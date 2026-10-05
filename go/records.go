// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// TableRows/1 as JsonEvents/1: an array of records keyed by label.
//
// A table has a natural JSON form, one object per row with the column
// labels as member names, and producing it as events rather than text
// means the JSON renderer, and every other JsonEvents/1 consumer, gets it
// for free. The stage retains the labels and nothing else: each row is
// emitted as it arrives and forgotten. A repeated label is resolved the
// way a JSON reader would resolve a repeated member in the
// un-deduplicated record, by keeping the last value that is there.

import (
	"fmt"
	"strconv"

	"github.com/tabnas/alchemy/go/shared"
)

// MissingRecord is what a CellMissing becomes in a record.
type MissingRecord uint8

const (
	// MissingSkip leaves the member out: the record says nothing where
	// the source had nothing. The default.
	MissingSkip MissingRecord = iota
	// MissingNull writes the member with a null value.
	MissingNull
	// MissingError fails the run with MISSING_VALUE.
	MissingError
)

// RecordsToJSON turns TableRows/1 into JsonEvents/1.
//
// Schema opens the array, each Row is one object whose members are the
// labels in schema order, End closes the array and ends the document. The
// protocol is validated as the CSV renderer validates it: one schema
// first, rows of the schema's width, one end, PROTOCOL_ORDER_ERROR
// otherwise. A schema with no columns is allowed, since an empty object
// is a JSON value. When a label repeats, each record carries it once,
// from the last column whose cell contributes a member (under MissingSkip
// a CellMissing contributes none), in that column's position. A failure
// found after events were forwarded is marked as having committed output,
// since the stage downstream may have rendered them.
type RecordsToJSON[S shared.Sink] struct {
	sink    S
	missing MissingRecord
	phase   phase
	labels  []string
	// nextSame is, per column, the next later column with the same
	// label, or -1 for the common case of a label that does not repeat.
	nextSame  []int
	rows      uint64
	forwarded bool
}

// NewRecordsToJSON is the stage in front of sink, skipping Missing cells.
func NewRecordsToJSON[S shared.Sink](sink S) *RecordsToJSON[S] {
	return &RecordsToJSON[S]{sink: sink}
}

// WithMissing sets the policy for CellMissing.
func (r *RecordsToJSON[S]) WithMissing(missing MissingRecord) *RecordsToJSON[S] {
	r.missing = missing
	return r
}

// Rows is the rows emitted so far.
func (r *RecordsToJSON[S]) Rows() uint64 { return r.rows }

// IsDone reports whether End has been forwarded and accepted.
func (r *RecordsToJSON[S]) IsDone() bool { return r.phase == done }

// Inner is the sink.
func (r *RecordsToJSON[S]) Inner() S { return r.sink }

func (r *RecordsToJSON[S]) fail(f *shared.Fail) *shared.Fail {
	if r.forwarded {
		f.Committed()
	}
	return f
}

func (r *RecordsToJSON[S]) send(ev shared.Event) (shared.Flow, *shared.Fail) {
	r.forwarded = true
	return r.sink.Event(ev)
}

// contributes reports whether a cell contributes a member to its record
// under the policy: every cell but a Missing that is skipped.
func (r *RecordsToJSON[S]) contributes(c *shared.Cell) bool {
	return !(c.IsMissing() && r.missing == MissingSkip)
}

func (r *RecordsToJSON[S]) schema(columns []shared.PublicColumn) (shared.Flow, *shared.Fail) {
	switch r.phase {
	case inRows:
		return shared.Continue, r.fail(shared.ProtocolFail("a second schema"))
	case done:
		return shared.Continue, r.fail(shared.ProtocolFail("a schema after the end"))
	}
	r.labels = make([]string, len(columns))
	for i, c := range columns {
		r.labels[i] = c.Label
	}
	r.nextSame = make([]int, len(r.labels))
	for i := range r.labels {
		r.nextSame[i] = -1
		for j := i + 1; j < len(r.labels); j++ {
			if r.labels[j] == r.labels[i] {
				r.nextSame[i] = j
				break
			}
		}
	}
	r.phase = inRows
	return r.send(shared.EvArrayStart())
}

func (r *RecordsToJSON[S]) row(cells []shared.Cell) (shared.Flow, *shared.Fail) {
	switch r.phase {
	case beforeSchema:
		return shared.Continue, shared.ProtocolFail("a row before the schema")
	case done:
		return shared.Continue, r.fail(shared.ProtocolFail("a row after the end"))
	}
	if len(cells) != len(r.labels) {
		return shared.Continue, r.fail(shared.ProtocolFail(fmt.Sprintf("row %d has %d cells; the schema has %d columns",
			r.rows+1, len(cells), len(r.labels))))
	}
	if r.missing == MissingError {
		// Before any of the row is forwarded, so a row is emitted whole or
		// not at all, as the CSV renderer renders it.
		for i := range cells {
			if cells[i].IsMissing() {
				return shared.Continue, r.fail(shared.NewFail(shared.CodeMissingValue, fmt.Sprintf(
					"row %d has no value for column %s", r.rows+1, strconv.Quote(r.labels[i]))))
			}
		}
	}
	if flow, f := r.send(shared.EvObjectStart()); f != nil || flow == shared.Stop {
		return flow, f
	}
	for i := range cells {
		cell := &cells[i]
		if !r.contributes(cell) {
			continue
		}
		// A repeated label is written from the last column whose cell
		// contributes; an earlier column's value is superseded only by a
		// member that will actually be there.
		later := r.nextSame[i]
		for later >= 0 && !r.contributes(&cells[later]) {
			later = r.nextSame[later]
		}
		if later >= 0 {
			continue
		}
		var value shared.Event
		switch cell.Kind {
		case shared.CellNull:
			value = shared.EvNull()
		case shared.CellBool:
			value = shared.EvBool(cell.Bool)
		case shared.CellNumber:
			value = shared.Event{Kind: shared.Number, HasLexeme: cell.HasLexeme, Value: cell.Value, Lexeme: cell.Lexeme}
		case shared.CellString:
			value = shared.EvString(cell.Text)
		case shared.CellMissing:
			// Skip does not contribute and was passed over above; Error was
			// rejected before the row began.
			if r.missing != MissingNull {
				continue
			}
			value = shared.EvNull()
		}
		if flow, f := r.send(shared.EvKey(r.labels[i])); f != nil || flow == shared.Stop {
			return flow, f
		}
		if flow, f := r.send(value); f != nil || flow == shared.Stop {
			return flow, f
		}
	}
	if flow, f := r.send(shared.EvObjectEnd()); f != nil || flow == shared.Stop {
		return flow, f
	}
	r.rows++
	return shared.Continue, nil
}

func (r *RecordsToJSON[S]) end() (shared.Flow, *shared.Fail) {
	switch r.phase {
	case beforeSchema:
		return shared.Continue, shared.ProtocolFail("the end before the schema")
	case done:
		return shared.Continue, r.fail(shared.ProtocolFail("a second end"))
	}
	if flow, f := r.send(shared.EvArrayEnd()); f != nil || flow == shared.Stop {
		return flow, f
	}
	// Done only once End has been taken downstream: a sink that failed on
	// it has not seen the document end.
	flow, f := r.send(shared.EvEnd())
	if f != nil {
		return flow, f
	}
	r.phase = done
	return flow, nil
}

// TableEvent turns one TableRows/1 event into JsonEvents/1.
func (r *RecordsToJSON[S]) TableEvent(ev shared.TableEvent) (shared.Flow, *shared.Fail) {
	switch ev.Kind {
	case shared.TableSchema:
		return r.schema(ev.Columns)
	case shared.TableRow:
		return r.row(ev.Cells)
	case shared.TableEnd:
		return r.end()
	}
	return shared.Continue, r.fail(shared.ProtocolFail(fmt.Sprintf("an unknown table event kind %d", ev.Kind)))
}
