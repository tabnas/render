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

	tt "github.com/tabnas/transduce/go"
)

// TextOut is a consumer of text fragments.
//
// Fragments arrive in order and are concatenated; where the boundaries
// fall carries no meaning. Flush pushes everything held so far to the
// final destination, and a renderer calls it exactly once, at the end of
// the protocol it renders, so that a document that failed half way is not
// flushed as if it were whole.
//
// HasCommitted reports whether any text has reached the final
// destination, so that a failure found now leaves partial output behind.
// A renderer asks it when it fails and reports CommittedOutput from the
// answer. An output that cannot tell answers true, the conservative
// answer (Rust's default method; Go interfaces have none, so every
// implementation says so itself).
type TextOut interface {
	WriteStr(s string) *tt.Fail
	Flush() *tt.Fail
	HasCommitted() bool
}

// DefaultBudget is the default coalescing budget of a WriteOut: large
// enough that a write per budget is negligible next to the parse, small
// enough to be invisible in a process's memory.
const DefaultBudget = 32 * 1024

// errWriteZero is a writer that took nothing and reported no error, which
// Rust's write_all calls WriteZero.
var errWriteZero = errors.New("failed to write whole buffer")

// WriteOut coalesces fragments and writes them to an io.Writer.
//
// Retention is bounded by the budget: the buffer never holds more than
// budget bytes, and a fragment at least as large as the budget goes to
// the writer directly, after whatever was buffered before it. A
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
	maxOutputBytes *uint64
	metrics        *tt.Metrics
	// accepted is the bytes taken in: buffered or written.
	accepted uint64
	// committed is the bytes the writer accepted, counted write by write.
	committed uint64
}

// NewWriteOut is a WriteOut over w with the default budget, no limit and
// no metrics.
func NewWriteOut(w io.Writer) *WriteOut {
	return &WriteOut{writer: w, budget: DefaultBudget}
}

// WithBudget sets the coalescing budget in bytes. Zero means every
// non-empty fragment is written as it arrives. A negative budget is zero.
func (o *WriteOut) WithBudget(budget int) *WriteOut {
	if budget < 0 {
		budget = 0
	}
	o.budget = budget
	return o
}

// WithLimits enforces limits.MaxOutputBytes; the other limits belong to
// the stages upstream.
func (o *WriteOut) WithLimits(limits tt.Limits) *WriteOut {
	if limits.MaxOutputBytes != nil {
		max := *limits.MaxOutputBytes
		o.maxOutputBytes = &max
	} else {
		o.maxOutputBytes = nil
	}
	return o
}

// WithMetrics counts OutputBytes into these metrics.
func (o *WriteOut) WithMetrics(metrics *tt.Metrics) *WriteOut {
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

func (o *WriteOut) failIO(err error) *tt.Fail {
	f := tt.OutputFail(fmt.Sprintf("writing the output failed: %v", err))
	if o.committed > 0 {
		f.Committed()
	}
	return f
}

// send writes b through Write in a loop, counting every write the writer
// accepted before going on to the next, so Committed equals what the
// writer holds whichever write failed. A write that takes nothing without
// an error is errWriteZero rather than a spin.
func (o *WriteOut) send(b []byte) *tt.Fail {
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

func (o *WriteOut) drain() *tt.Fail {
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
func (o *WriteOut) WriteStr(s string) *tt.Fail {
	n := uint64(len(s))
	if o.maxOutputBytes != nil {
		max := *o.maxOutputBytes
		if o.accepted+n > max || o.accepted+n < o.accepted {
			f := tt.LimitFail("max_output_bytes", max, fmt.Sprintf(
				"the output would exceed %d bytes: %d written, %d more", max, o.accepted, n))
			if o.committed > 0 {
				f.Committed()
			}
			return f
		}
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
func (o *WriteOut) Flush() *tt.Fail {
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

// StringOut is a TextOut that keeps the text, for tests and small
// results.
type StringOut struct {
	text strings.Builder
}

// NewStringOut is an empty StringOut.
func NewStringOut() *StringOut { return &StringOut{} }

// WriteStr appends s.
func (s *StringOut) WriteStr(t string) *tt.Fail {
	s.text.WriteString(t)
	return nil
}

// Flush does nothing: the string is the destination.
func (s *StringOut) Flush() *tt.Fail { return nil }

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
func (j *Join[O]) ItemStart() *tt.Fail {
	if j.inItem {
		return tt.ProtocolFail("join: an item started inside an item that has not ended")
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
func (j *Join[O]) ItemEnd() *tt.Fail {
	if !j.inItem {
		return tt.ProtocolFail("join: an item ended when none was open")
	}
	j.inItem = false
	return nil
}

// Items is the items begun so far.
func (j *Join[O]) Items() uint64 { return j.items }

// Inner is the output beneath.
func (j *Join[O]) Inner() O { return j.out }

// WriteStr writes s inside the open item, or as an item of its own.
func (j *Join[O]) WriteStr(s string) *tt.Fail {
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
func (j *Join[O]) Flush() *tt.Fail { return j.out.Flush() }

// HasCommitted asks the output beneath.
func (j *Join[O]) HasCommitted() bool { return j.out.HasCommitted() }

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
func (c *Concat[O]) ItemStart() *tt.Fail { return c.join.ItemStart() }

// ItemEnd ends the current item; see Join.ItemEnd.
func (c *Concat[O]) ItemEnd() *tt.Fail { return c.join.ItemEnd() }

// Items is the items begun so far.
func (c *Concat[O]) Items() uint64 { return c.join.Items() }

// Inner is the output beneath.
func (c *Concat[O]) Inner() O { return c.join.out }

// WriteStr appends s.
func (c *Concat[O]) WriteStr(s string) *tt.Fail { return c.join.WriteStr(s) }

// Flush flushes the output beneath.
func (c *Concat[O]) Flush() *tt.Fail { return c.join.Flush() }

// HasCommitted asks the output beneath.
func (c *Concat[O]) HasCommitted() bool { return c.join.HasCommitted() }

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
type ReplaceText[O TextOut] struct {
	out   O
	from  string
	to    string
	carry string
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

func (r *ReplaceText[O]) scan(text string) *tt.Fail {
	rest := text
	for {
		i := strings.Index(rest, r.from)
		if i < 0 {
			break
		}
		if i > 0 {
			if f := r.out.WriteStr(rest[:i]); f != nil {
				return f
			}
		}
		if r.to != "" {
			if f := r.out.WriteStr(r.to); f != nil {
				return f
			}
		}
		rest = rest[i+len(r.from):]
	}
	keep := r.pendingLen(rest)
	emit := rest[:len(rest)-keep]
	if emit != "" {
		if f := r.out.WriteStr(emit); f != nil {
			return f
		}
	}
	// A copy, so the carry never pins the fragment it was cut from.
	r.carry = strings.Clone(rest[len(rest)-keep:])
	return nil
}

// WriteStr takes one fragment.
func (r *ReplaceText[O]) WriteStr(s string) *tt.Fail {
	if r.from == "" {
		return r.out.WriteStr(s)
	}
	if r.carry == "" {
		return r.scan(s)
	}
	text := r.carry + s
	r.carry = ""
	return r.scan(text)
}

// Flush writes the carry out, then flushes the output beneath.
func (r *ReplaceText[O]) Flush() *tt.Fail {
	if r.carry != "" {
		pending := r.carry
		r.carry = ""
		if f := r.out.WriteStr(pending); f != nil {
			return f
		}
	}
	return r.out.Flush()
}

// HasCommitted asks the output beneath: the carry has not gone anywhere.
func (r *ReplaceText[O]) HasCommitted() bool { return r.out.HasCommitted() }
