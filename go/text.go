// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// Text output: where rendered fragments go.
//
// A renderer produces many small fragments (a quote, a field, a comma)
// and must never hold a whole document. TextOut is the boundary: a
// fragment in, a failure out. WriteOut coalesces fragments to a byte
// budget before they reach an io.Writer, so a renderer can write a
// character at a time without paying a system call for each, and it is
// where the output limit and the output-bytes metric live, because it is
// the one stage that knows what actually left. Join and ReplaceText are
// the two text combinators whose correctness depends on the difference
// between a logical item and a transport chunk.

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/tabnas/alchemy/go/shared"
)

// DefaultBudget is the default coalescing budget of a WriteOut: large
// enough that a write per budget is negligible next to the parse, small
// enough to be invisible in a process's memory.
const DefaultBudget = 32 * 1024

// DefaultHold is the default hold of a WriteOut: the longest record it
// keeps back until the record's end is marked. A record longer than this
// is written as it comes, so that one huge record (a document whose root
// holds one value) costs a bounded buffer rather than its whole size.
const DefaultHold = 16 * 1024 * 1024

// errWriteZero is a writer that took nothing and reported no error, which
// Rust's write_all calls WriteZero.
var errWriteZero = errors.New("failed to write whole buffer")

// WriteOut coalesces fragments and writes them to an io.Writer.
//
// Until a record end is marked (RecordEnd), retention is bounded by the
// budget: the buffer never holds more than budget bytes, and a fragment
// at least as large as the budget goes to the writer directly, after
// whatever was buffered before it. From the first record end on, the
// buffer is drained only at a record end, once it holds at least budget
// bytes, so the text a run leaves behind when it fails part way ends with
// a whole record: a failure drops the buffer, and with it the record that
// was being written. A record that outgrows the hold (DefaultHold unless
// WithHold says) is written as it comes, the fragment that outgrows it
// going to the writer directly after the buffer, and a failure inside it
// cuts it; the buffer never holds more of an open record than the hold, so
// retention is bounded by the budget and the hold together. A
// zero-length fragment is never a write. The output limit is checked on
// every fragment BEFORE it is accepted, counting the bytes buffered as
// well as the bytes written (UTF-8 bytes, as Go strings are), so a run
// that would exceed max_output_bytes fails without emitting the fragment
// that crossed the line. OutputBytes in the shared Metrics counts bytes
// the writer accepted; like Committed, it is kept per Write call, so the
// part of a buffer a writer took before failing is counted.
//
// Flush drains the buffer and then, when the writer has a
// `Flush() error` method (a *bufio.Writer), calls it.
type WriteOut struct {
	writer         io.Writer
	buf            []byte
	budget         int
	hold           int
	maxOutputBytes *uint64
	metrics        *shared.Metrics
	// accepted is the bytes taken in: buffered or written.
	accepted uint64
	// committed is the bytes the writer accepted, counted write by write.
	committed uint64
	// records says a record end has been marked: from the first, the
	// buffer is drained only at a record end.
	records bool
	// mark is where the open record begins in the buffer: the bytes
	// before it are whole records.
	mark int
}

// NewWriteOut is a WriteOut over w with the default budget and hold, no
// limit and no metrics.
func NewWriteOut(w io.Writer) *WriteOut {
	return &WriteOut{writer: w, budget: DefaultBudget, hold: DefaultHold}
}

// WithBudget sets the coalescing budget in bytes. Zero means every
// non-empty fragment is written as it arrives; once records are marked,
// it means every record is written as it ends. A negative budget is zero.
func (o *WriteOut) WithBudget(budget int) *WriteOut {
	if budget < 0 {
		budget = 0
	}
	o.budget = budget
	return o
}

// WithHold sets the longest record, in bytes, held back until its end is
// marked; a longer one is written as it comes. Zero holds nothing, so
// marks change nothing. A negative hold is zero.
func (o *WriteOut) WithHold(hold int) *WriteOut {
	if hold < 0 {
		hold = 0
	}
	o.hold = hold
	return o
}

// WithLimits enforces limits.MaxOutputBytes; the other limits belong to
// the stages upstream.
func (o *WriteOut) WithLimits(limits shared.Limits) *WriteOut {
	if limits.MaxOutputBytes != nil {
		max := *limits.MaxOutputBytes
		o.maxOutputBytes = &max
	} else {
		o.maxOutputBytes = nil
	}
	return o
}

// WithMetrics counts OutputBytes into these metrics.
func (o *WriteOut) WithMetrics(metrics *shared.Metrics) *WriteOut {
	o.metrics = metrics
	return o
}

// Accepted is the bytes accepted so far, buffered or written.
func (o *WriteOut) Accepted() uint64 { return o.accepted }

// Committed is the bytes the writer accepted, including the bytes of a
// short write that a failure cut off.
func (o *WriteOut) Committed() uint64 { return o.committed }

// Inner hands the writer back WITHOUT flushing. Whatever the buffer still
// holds is never written, so the writer holds exactly the bytes Committed
// counts: a document that failed before its End does not reach the
// writer on the way out. A caller that wants a partial output anyway
// calls Flush first, knowingly. The WriteOut should not be used after.
func (o *WriteOut) Inner() io.Writer { return o.writer }

func (o *WriteOut) failIO(err error) *shared.Fail {
	f := shared.OutputFail(fmt.Sprintf("writing the output failed: %v", err))
	if o.committed > 0 {
		f.Committed()
	}
	return f
}

// send writes b through Write in a loop, counting every write the writer
// accepted before going on to the next, so Committed equals what the
// writer holds whichever write failed. A write that takes nothing without
// an error is errWriteZero rather than a spin.
func (o *WriteOut) send(b []byte) *shared.Fail {
	for len(b) > 0 {
		n, err := o.writer.Write(b)
		if n < 0 {
			n = 0
		}
		if n > len(b) {
			n = len(b)
		}
		if n > 0 {
			o.committed += uint64(n)
			if o.metrics != nil {
				o.metrics.OutputBytes.Add(uint64(n))
			}
			b = b[n:]
		}
		if err != nil {
			// The buffer is not retried: a failed writer is done.
			o.buf = o.buf[:0]
			return o.failIO(err)
		}
		if n == 0 {
			o.buf = o.buf[:0]
			return o.failIO(errWriteZero)
		}
	}
	return nil
}

// drain sends the whole buffer, which holds no open record afterwards: it
// is empty whether the send succeeded or failed.
func (o *WriteOut) drain() *shared.Fail {
	o.mark = 0
	if len(o.buf) == 0 {
		return nil
	}
	if f := o.send(o.buf); f != nil {
		return f
	}
	// Keep the allocation: the buffer refills up to the budget again.
	o.buf = o.buf[:0]
	return nil
}

// WriteStr takes one fragment.
func (o *WriteOut) WriteStr(s string) *shared.Fail {
	n := uint64(len(s))
	if o.maxOutputBytes != nil {
		max := *o.maxOutputBytes
		if o.accepted+n > max || o.accepted+n < o.accepted {
			f := shared.LimitFail("max_output_bytes", max, fmt.Sprintf(
				"the output would exceed %d bytes: %d written, %d more", max, o.accepted, n))
			if o.committed > 0 {
				f.Committed()
			}
			return f
		}
	}
	if o.records {
		// The open record is held until its end. One that outgrows the
		// hold is written as it comes: the buffer goes first, then the
		// fragment that outgrew it, which the buffer never takes, so the
		// buffer never grows past the hold.
		if len(o.buf)-o.mark+len(s) > o.hold {
			if f := o.drain(); f != nil {
				return f
			}
			if len(s) > 0 {
				if f := o.send([]byte(s)); f != nil {
					return f
				}
			}
		} else {
			o.buf = append(o.buf, s...)
		}
		o.accepted += n
		return nil
	}
	if len(o.buf)+len(s) > o.budget {
		if f := o.drain(); f != nil {
			return f
		}
	}
	if len(s) >= o.budget {
		if len(s) > 0 {
			if f := o.send([]byte(s)); f != nil {
				return f
			}
		}
	} else {
		o.buf = append(o.buf, s...)
	}
	o.accepted += n
	return nil
}

// Flush drains the buffer to the writer, then flushes the writer when it
// can be flushed.
func (o *WriteOut) Flush() *shared.Fail {
	if f := o.drain(); f != nil {
		return f
	}
	if fl, ok := o.writer.(interface{ Flush() error }); ok {
		if err := fl.Flush(); err != nil {
			return o.failIO(err)
		}
	}
	return nil
}

// HasCommitted answers from the bytes the writer received: a fragment
// that is still buffered is not committed.
func (o *WriteOut) HasCommitted() bool { return o.committed > 0 }

// RecordEnd says the buffer now ends with a whole record: it is drained
// if it holds the budget, and kept otherwise, the open record starting
// after it.
func (o *WriteOut) RecordEnd() *shared.Fail {
	o.records = true
	if len(o.buf) >= o.budget {
		return o.drain()
	}
	o.mark = len(o.buf)
	return nil
}

// StringOut is a TextOut that keeps the text, for tests and small
// results.
type StringOut struct {
	text strings.Builder
}

// NewStringOut is an empty StringOut.
func NewStringOut() *StringOut { return &StringOut{} }

// WriteStr appends s.
func (s *StringOut) WriteStr(t string) *shared.Fail {
	s.text.WriteString(t)
	return nil
}

// Flush does nothing: the string is the destination.
func (s *StringOut) Flush() *shared.Fail { return nil }

// HasCommitted reports whether the string holds any text: it is the
// destination, so its text is committed as soon as it is there.
func (s *StringOut) HasCommitted() bool { return s.text.Len() > 0 }

// String is the text so far.
func (s *StringOut) String() string { return s.text.String() }

// Len is the bytes held.
func (s *StringOut) Len() int { return s.text.Len() }

// Join writes a separator between logical items.
//
// An item is what lies between ItemStart and ItemEnd; it may be written in
// any number of fragments, or in none, and an empty item is still an
// item, so two empty items joined with "," are ",". A fragment written
// outside an item is an item of its own. The separator goes before every
// item but the first, never between the fragments of one item.
type Join[O TextOut] struct {
	out       O
	separator string
	items     uint64
	inItem    bool
}

// NewJoin is a Join over out.
func NewJoin[O TextOut](out O, separator string) *Join[O] {
	return &Join[O]{out: out, separator: separator}
}

// ItemStart begins an item: the separator is written now if an item came
// before. Starting an item inside an item is PROTOCOL_ORDER_ERROR.
func (j *Join[O]) ItemStart() *shared.Fail {
	if j.inItem {
		return shared.ProtocolFail("join: an item started inside an item that has not ended")
	}
	if j.items > 0 && j.separator != "" {
		if f := j.out.WriteStr(j.separator); f != nil {
			return f
		}
	}
	j.items++
	j.inItem = true
	return nil
}

// ItemEnd ends the current item. Ending when no item is open is
// PROTOCOL_ORDER_ERROR.
func (j *Join[O]) ItemEnd() *shared.Fail {
	if !j.inItem {
		return shared.ProtocolFail("join: an item ended when none was open")
	}
	j.inItem = false
	return nil
}

// Items is the items begun so far.
func (j *Join[O]) Items() uint64 { return j.items }

// Inner is the output beneath.
func (j *Join[O]) Inner() O { return j.out }

// WriteStr writes s inside the open item, or as an item of its own.
func (j *Join[O]) WriteStr(s string) *shared.Fail {
	if j.inItem {
		return j.out.WriteStr(s)
	}
	if f := j.ItemStart(); f != nil {
		return f
	}
	if f := j.out.WriteStr(s); f != nil {
		return f
	}
	return j.ItemEnd()
}

// Flush flushes the output beneath; an open item stays open, since a
// flush is about transport and an item is about meaning.
func (j *Join[O]) Flush() *shared.Fail { return j.out.Flush() }

// HasCommitted asks the output beneath.
func (j *Join[O]) HasCommitted() bool { return j.out.HasCommitted() }

// RecordEnd is passed on as it is: the separator of the next item is
// written when that item starts, after the mark, so it belongs to the
// next record.
func (j *Join[O]) RecordEnd() *shared.Fail { return shared.RecordEnd(j.out) }

// Concat is concatenation: a Join with no separator, under the name the
// design brief and the language give it. The item markers are accepted
// and counted all the same, so the interpreter's concat and join share
// one shape.
type Concat[O TextOut] struct {
	join Join[O]
}

// NewConcat is a Concat over out.
func NewConcat[O TextOut](out O) *Concat[O] {
	return &Concat[O]{join: Join[O]{out: out}}
}

// ItemStart begins an item; see Join.ItemStart.
func (c *Concat[O]) ItemStart() *shared.Fail { return c.join.ItemStart() }

// ItemEnd ends the current item; see Join.ItemEnd.
func (c *Concat[O]) ItemEnd() *shared.Fail { return c.join.ItemEnd() }

// Items is the items begun so far.
func (c *Concat[O]) Items() uint64 { return c.join.Items() }

// Inner is the output beneath.
func (c *Concat[O]) Inner() O { return c.join.out }

// WriteStr appends s.
func (c *Concat[O]) WriteStr(s string) *shared.Fail { return c.join.WriteStr(s) }

// Flush flushes the output beneath.
func (c *Concat[O]) Flush() *shared.Fail { return c.join.Flush() }

// HasCommitted asks the output beneath.
func (c *Concat[O]) HasCommitted() bool { return c.join.HasCommitted() }

// RecordEnd is passed on; see Join.RecordEnd.
func (c *Concat[O]) RecordEnd() *shared.Fail { return c.join.RecordEnd() }

// ReplaceText replaces every occurrence of a fixed literal, across
// fragment boundaries.
//
// The text is treated as one string however it is chunked, with the same
// left-to-right, non-overlapping matches as strings.ReplaceAll. To do
// that without holding the text, at most len(from)-1 bytes are carried
// from one fragment to the next: the longest suffix of what has been seen
// that could still begin a match. Flush writes the carry out, because
// nothing can complete it once the caller has declared the text at a
// boundary. An empty literal matches nothing and the text passes through
// unchanged.
//
// A record end is no boundary for a match, so marks change no text.
// Record ends that arrive while bytes are carried wait with them, each at
// its own offset, and are passed on where they fall once the bytes are
// written: before a match that begins there, after one that ends there.
// One that falls inside a match is dropped, the two records it parted
// ending together at the next mark. At most one waits at each offset of
// the carry, so they are as bounded as the carry is.
type ReplaceText[O TextOut] struct {
	out   O
	from  string
	to    string
	carry string
	// marks are record ends at these offsets into the carry, ascending,
	// waiting for the carried bytes to be written.
	marks []int
}

// NewReplaceText is a ReplaceText over out.
func NewReplaceText[O TextOut](out O, from, to string) *ReplaceText[O] {
	return &ReplaceText[O]{out: out, from: from, to: to}
}

// Inner is the output beneath.
func (r *ReplaceText[O]) Inner() O { return r.out }

// pendingLen is the longest proper prefix of the literal that rest ends
// with, in bytes; zero when there is none. Only character boundaries of
// the literal are tried: a match ending inside a character is impossible
// between two valid strings.
func (r *ReplaceText[O]) pendingLen(rest string) int {
	max := len(r.from) - 1
	if len(rest) < max {
		max = len(rest)
	}
	for k := max; k >= 1; k-- {
		if utf8.RuneStart(r.from[k]) && strings.HasSuffix(rest, r.from[:k]) {
			return k
		}
	}
	return 0
}

// emit writes s, which begins at bytes into the text being scanned, with
// the waiting record ends from marks[next] on where they fall within it,
// its end included. It answers the index of the first mark still waiting.
func (r *ReplaceText[O]) emit(s string, at int, marks []int, next int) (int, *shared.Fail) {
	from := 0
	for ; next < len(marks) && marks[next] <= at+len(s); next++ {
		cut := marks[next] - at
		if cut < from {
			cut = from
		}
		if piece := s[from:cut]; piece != "" {
			if f := r.out.WriteStr(piece); f != nil {
				return next, f
			}
		}
		if f := shared.RecordEnd(r.out); f != nil {
			return next, f
		}
		from = cut
	}
	if rest := s[from:]; rest != "" {
		if f := r.out.WriteStr(rest); f != nil {
			return next, f
		}
	}
	return next, nil
}

// scan replaces the matches in text, with record ends waiting at marks,
// and carries what could still begin one.
func (r *ReplaceText[O]) scan(text string, marks []int) *shared.Fail {
	next := 0
	rest := text
	at := 0
	var f *shared.Fail
	for {
		i := strings.Index(rest, r.from)
		if i < 0 {
			break
		}
		if next, f = r.emit(rest[:i], at, marks, next); f != nil {
			return f
		}
		start := at + i
		end := start + len(r.from)
		for next < len(marks) && marks[next] > start && marks[next] < end {
			next++
		}
		if r.to != "" {
			if f := r.out.WriteStr(r.to); f != nil {
				return f
			}
		}
		rest = rest[i+len(r.from):]
		at = end
	}
	keep := r.pendingLen(rest)
	written := rest[:len(rest)-keep]
	if next, f = r.emit(written, at, marks, next); f != nil {
		return f
	}
	// A copy, so the carry never pins the fragment it was cut from.
	r.carry = strings.Clone(rest[len(rest)-keep:])
	// The marks still waiting lie inside the carry, and move with it.
	base := at + len(written)
	waiting := marks[:0]
	for _, m := range marks[next:] {
		waiting = append(waiting, m-base)
	}
	r.marks = waiting
	return nil
}

// WriteStr takes one fragment.
func (r *ReplaceText[O]) WriteStr(s string) *shared.Fail {
	if r.from == "" {
		return r.out.WriteStr(s)
	}
	marks := r.marks
	r.marks = nil
	if r.carry == "" {
		return r.scan(s, marks)
	}
	text := r.carry + s
	r.carry = ""
	return r.scan(text, marks)
}

// Flush writes the carry out, then flushes the output beneath.
func (r *ReplaceText[O]) Flush() *shared.Fail {
	if r.carry != "" {
		pending, marks := r.carry, r.marks
		r.carry, r.marks = "", nil
		if _, f := r.emit(pending, 0, marks, 0); f != nil {
			return f
		}
	}
	return r.out.Flush()
}

// HasCommitted asks the output beneath: the carry has not gone anywhere.
func (r *ReplaceText[O]) HasCommitted() bool { return r.out.HasCommitted() }

// RecordEnd is passed on at once when nothing is carried; otherwise it
// waits at the carry's end, beside any that wait already, for the carried
// bytes to be written.
func (r *ReplaceText[O]) RecordEnd() *shared.Fail {
	if r.carry == "" {
		return shared.RecordEnd(r.out)
	}
	at := len(r.carry)
	if n := len(r.marks); n == 0 || r.marks[n-1] != at {
		r.marks = append(r.marks, at)
	}
	return nil
}
