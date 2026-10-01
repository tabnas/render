// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// JsonEvents/1 as JSON text.
//
// The renderer writes what it is given, in the order it is given, with
// nothing held back but the separators: a comma is written when the next
// item begins, never speculatively, so an aborted document is still a
// prefix of a valid one. Compact output is the standard profile; a fixed
// indent is a separate profile for people rather than programs, and the
// two differ only in whitespace. Strings are escaped as RFC 8259 requires
// and no more: `"`, `\`, and the control characters, with everything
// else, non-ASCII included, written as itself.
//
// The renderer validates the event sequence as it goes: one root value,
// keys only where a member begins, balanced containers, one end.

import (
	"fmt"
	"strings"

	tt "github.com/tabnas/transduce/go"
)

// JSONOptions is the JSON profile.
type JSONOptions struct {
	// Indent is spaces per nesting level, with a newline before every
	// item and every closing bracket of a non-empty container. Zero (or
	// less) is compact: no whitespace at all.
	Indent int
	// TrailingNewline writes a newline after the root value, at End.
	TrailingNewline bool
}

type frame struct {
	object       bool
	first        bool
	expectingKey bool
}

// JSONRenderer renders JsonEvents/1 as JSON text.
//
// Exactly one root value, then End; a second root, an End before the root
// or with a container open, a key outside an object or where a value is
// due, a value where a key is due, an unbalanced or mismatched close, and
// any event after End are PROTOCOL_ORDER_ERROR. Numbers write their
// lexeme when it is a JSON number (INVALID_NUMBER otherwise) and the
// shortest text that reads back as the value when there is none; NaN and
// infinity are TARGET_VALUE_UNREPRESENTABLE, whatever lexeme stands
// beside them. A number is checked before its separator is written, so a
// rejected value leaves no trace. The output is flushed once, at End; a
// failure found after any text was written says so with CommittedOutput.
type JSONRenderer[O TextOut] struct {
	out      O
	options  JSONOptions
	indent   int
	stack    []frame
	rootDone bool
	ended    bool
	emitted  bool
	scratch  []byte
	// pad is spaces, grown to the widest indentation written so far.
	pad string
}

// NewJSONRenderer is a renderer over out.
func NewJSONRenderer[O TextOut](out O, options JSONOptions) *JSONRenderer[O] {
	indent := options.Indent
	if indent < 0 {
		indent = 0
	}
	return &JSONRenderer[O]{out: out, options: options, indent: indent}
}

// Options is the profile.
func (r *JSONRenderer[O]) Options() JSONOptions { return r.options }

// Depth is the containers currently open.
func (r *JSONRenderer[O]) Depth() int { return len(r.stack) }

// IsDone reports whether End has been rendered and flushed.
func (r *JSONRenderer[O]) IsDone() bool { return r.ended }

// Inner is the output.
func (r *JSONRenderer[O]) Inner() O { return r.out }

func (r *JSONRenderer[O]) fail(f *tt.Fail) *tt.Fail {
	if r.emitted && r.out.HasCommitted() {
		f.Committed()
	}
	return f
}

func (r *JSONRenderer[O]) protocol(message string) *tt.Fail {
	return r.fail(tt.ProtocolFail(message))
}

func (r *JSONRenderer[O]) put(s string) *tt.Fail {
	r.emitted = true
	return r.out.WriteStr(s)
}

// putString writes the escaped form of s, streamed: each run that needs
// no escaping is written as it is and each escape as it comes, so the
// renderer never holds a copy of a scalar. Every byte that needs an
// escape is ASCII, so the scan is by byte; a multi-byte character is
// never split.
func (r *JSONRenderer[O]) putString(s string) *tt.Fail {
	if f := r.put(`"`); f != nil {
		return f
	}
	start := 0
	for i := 0; i < len(s); i++ {
		e := escape(s[i])
		if e == "" {
			continue
		}
		if i > start {
			if f := r.put(s[start:i]); f != nil {
				return f
			}
		}
		if f := r.put(e); f != nil {
			return f
		}
		start = i + 1
	}
	if start < len(s) {
		if f := r.put(s[start:]); f != nil {
			return f
		}
	}
	return r.put(`"`)
}

// putNumber writes a number checkNumber has passed: the lexeme as it is,
// or the value formatted once into the reused scratch buffer.
func (r *JSONRenderer[O]) putNumber(ev tt.Event) *tt.Fail {
	if ev.Lexeme != "" {
		return r.put(ev.Lexeme)
	}
	r.scratch = appendValue(r.scratch[:0], ev.Value)
	return r.put(string(r.scratch))
}

// breakLine writes a line break and the indentation of depth levels;
// nothing when compact.
func (r *JSONRenderer[O]) breakLine(depth int) *tt.Fail {
	if r.indent == 0 {
		return nil
	}
	width := depth * r.indent
	if len(r.pad) < width {
		r.pad = strings.Repeat(" ", width)
	}
	if f := r.put("\n"); f != nil {
		return f
	}
	return r.put(r.pad[:width])
}

// beginValue writes the separators before a value, after checking that
// one may begin.
func (r *JSONRenderer[O]) beginValue() *tt.Fail {
	if r.ended {
		return r.protocol("a value after the end")
	}
	depth := len(r.stack)
	if depth == 0 {
		if r.rootDone {
			return r.protocol("a second root value")
		}
		return nil
	}
	top := &r.stack[depth-1]
	if top.object {
		if top.expectingKey {
			return r.protocol("a value where a key is due")
		}
		return nil
	}
	comma := !top.first
	top.first = false
	if comma {
		if f := r.put(","); f != nil {
			return f
		}
	}
	return r.breakLine(depth)
}

// endValue is the bookkeeping after a whole value.
func (r *JSONRenderer[O]) endValue() {
	n := len(r.stack)
	if n == 0 {
		r.rootDone = true
		return
	}
	if r.stack[n-1].object {
		r.stack[n-1].expectingKey = true
	}
}

func (r *JSONRenderer[O]) key(k string) *tt.Fail {
	if r.ended {
		return r.protocol("a key after the end")
	}
	depth := len(r.stack)
	if depth == 0 {
		return r.protocol("a key outside an object")
	}
	top := &r.stack[depth-1]
	switch {
	case !top.object:
		return r.protocol("a key inside an array")
	case !top.expectingKey:
		return r.protocol("a key where a value is due")
	}
	comma := !top.first
	top.first = false
	if comma {
		if f := r.put(","); f != nil {
			return f
		}
	}
	if f := r.breakLine(depth); f != nil {
		return f
	}
	if f := r.putString(k); f != nil {
		return f
	}
	sep := ":"
	if r.indent > 0 {
		sep = ": "
	}
	if f := r.put(sep); f != nil {
		return f
	}
	// Only after the key is on the wire, so a write failure cannot leave
	// the frame half updated.
	r.stack[depth-1].expectingKey = false
	return nil
}

func (r *JSONRenderer[O]) start(open string, fr frame) *tt.Fail {
	if f := r.beginValue(); f != nil {
		return f
	}
	if f := r.put(open); f != nil {
		return f
	}
	r.stack = append(r.stack, fr)
	return nil
}

func (r *JSONRenderer[O]) closeObject() *tt.Fail {
	if r.ended {
		return r.protocol("an object end after the end")
	}
	n := len(r.stack)
	if n == 0 {
		return r.protocol("an object end with no open object")
	}
	top := r.stack[n-1]
	switch {
	case !top.object:
		return r.protocol("an object end inside an array")
	case !top.expectingKey:
		return r.protocol("an object ended after a key with no value")
	}
	r.stack = r.stack[:n-1]
	if !top.first {
		if f := r.breakLine(len(r.stack)); f != nil {
			return f
		}
	}
	if f := r.put("}"); f != nil {
		return f
	}
	r.endValue()
	return nil
}

func (r *JSONRenderer[O]) closeArray() *tt.Fail {
	if r.ended {
		return r.protocol("an array end after the end")
	}
	n := len(r.stack)
	if n == 0 {
		return r.protocol("an array end with no open array")
	}
	top := r.stack[n-1]
	if top.object {
		return r.protocol("an array end inside an object")
	}
	r.stack = r.stack[:n-1]
	if !top.first {
		if f := r.breakLine(len(r.stack)); f != nil {
			return f
		}
	}
	if f := r.put("]"); f != nil {
		return f
	}
	r.endValue()
	return nil
}

func (r *JSONRenderer[O]) scalar(ev tt.Event) *tt.Fail {
	// Before the separator: a number that will be refused must leave
	// nothing behind, or a caller that carries on after the failure would
	// find `[1,,2]` in the output.
	if ev.Kind == tt.Number {
		if f := checkNumber(ev.Value, ev.Lexeme); f != nil {
			return r.fail(f)
		}
	}
	if f := r.beginValue(); f != nil {
		return f
	}
	var f *tt.Fail
	switch ev.Kind {
	case tt.Null:
		f = r.put("null")
	case tt.Bool:
		if ev.Bool {
			f = r.put("true")
		} else {
			f = r.put("false")
		}
	case tt.Number:
		f = r.putNumber(ev)
	case tt.String:
		f = r.putString(ev.Text)
	}
	if f != nil {
		return f
	}
	r.endValue()
	return nil
}

func (r *JSONRenderer[O]) end() *tt.Fail {
	if r.ended {
		return r.protocol("a second end")
	}
	if len(r.stack) > 0 {
		return r.protocol(fmt.Sprintf("the end with %d open container(s)", len(r.stack)))
	}
	if !r.rootDone {
		return r.protocol("the end before a root value")
	}
	if r.options.TrailingNewline {
		if f := r.put("\n"); f != nil {
			return f
		}
	}
	// Ended only once the flush has succeeded: a document whose last
	// bytes never reached the writer is not done, whatever End said.
	if f := r.out.Flush(); f != nil {
		return f
	}
	r.ended = true
	return nil
}

// control is the \u00XX forms of the control characters, the short
// escapes RFC 8259 names among them, indexed by code point.
var control = [32]string{
	`\u0000`, `\u0001`, `\u0002`, `\u0003`, `\u0004`, `\u0005`, `\u0006`, `\u0007`, `\b`,
	`\t`, `\n`, `\u000b`, `\f`, `\r`, `\u000e`, `\u000f`, `\u0010`, `\u0011`, `\u0012`,
	`\u0013`, `\u0014`, `\u0015`, `\u0016`, `\u0017`, `\u0018`, `\u0019`, `\u001a`,
	`\u001b`, `\u001c`, `\u001d`, `\u001e`, `\u001f`,
}

// escape is the escape RFC 8259 requires for byte c, or "" when it is
// written as itself: `"`, `\` and the control characters, and no more.
func escape(c byte) string {
	switch {
	case c == '"':
		return `\"`
	case c == '\\':
		return `\\`
	case c < 0x20:
		return control[c]
	}
	return ""
}

// Event renders one JsonEvents/1 event.
func (r *JSONRenderer[O]) Event(ev tt.Event) (tt.Flow, *tt.Fail) {
	var f *tt.Fail
	switch ev.Kind {
	case tt.ObjectStart:
		f = r.start("{", frame{object: true, first: true, expectingKey: true})
	case tt.ArrayStart:
		f = r.start("[", frame{first: true})
	case tt.ObjectEnd:
		f = r.closeObject()
	case tt.ArrayEnd:
		f = r.closeArray()
	case tt.Key:
		f = r.key(ev.Text)
	case tt.Null, tt.Bool, tt.Number, tt.String:
		f = r.scalar(ev)
	case tt.End:
		f = r.end()
	default:
		f = r.protocol(fmt.Sprintf("an unknown event kind %s", ev.Kind))
	}
	return tt.Continue, f
}
