// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// rs/src/json.rs's tests: the profiles, escaping, numbers, and every JSON
// protocol error.

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	tt "github.com/tabnas/transduce/go"
)

func jnum(lexeme string) tt.Event {
	v, _ := strconv.ParseFloat(lexeme, 64)
	if !IsJSONNumber(lexeme) {
		v = 0
	}
	return tt.EvNumberLexeme(v, lexeme)
}

var (
	oS   = tt.EvObjectStart()
	oE   = tt.EvObjectEnd()
	aS   = tt.EvArrayStart()
	aE   = tt.EvArrayEnd()
	jEnd = tt.EvEnd()
	jNul = tt.EvNull()
)

func key(k string) tt.Event        { return tt.EvKey(k) }
func str(v string) tt.Event        { return tt.EvString(v) }
func jb(b bool) tt.Event           { return tt.EvBool(b) }
func jv(v float64) tt.Event        { return tt.EvNumber(v) }
func evs(e ...tt.Event) []tt.Event { return e }

func renderJSON(options JSONOptions, events []tt.Event) (string, *tt.Fail) {
	r := NewJSONRenderer(NewStringOut(), options)
	for _, ev := range events {
		if _, f := r.Event(ev); f != nil {
			return r.Inner().String(), f
		}
	}
	return r.Inner().String(), nil
}

func compact(events ...tt.Event) (string, *tt.Fail) { return renderJSON(JSONOptions{}, events) }

func indented(t *testing.T, n int, events ...tt.Event) string {
	t.Helper()
	out, f := renderJSON(JSONOptions{Indent: n}, events)
	ok(t, f)
	return out
}

var doc = []tt.Event{
	oS, key("a"), aS, tt.EvNumberLexeme(1, "1"), jv(2.5), str("x"), jb(true), jNul, aE,
	key("b"), oS, oE, key("c"), aS, aE, key("d"), oS, key("e"), jb(false), oE, oE, jEnd,
}

func TestCompactOutputHasNoWhitespace(t *testing.T) {
	out, f := compact(doc...)
	ok(t, f)
	eq(t, out, `{"a":[1,2.5,"x",true,null],"b":{},"c":[],"d":{"e":false}}`, "json")
}

func TestAnIndentWritesFixedNestingAndKeepsEmptyContainersOnOneLine(t *testing.T) {
	eq(t, indented(t, 2, doc...),
		"{\n  \"a\": [\n    1,\n    2.5,\n    \"x\",\n    true,\n    null\n  ],\n  \"b\": {},\n  \"c\": [],\n  \"d\": {\n    \"e\": false\n  }\n}", "json")
	eq(t, indented(t, 4, aS, aS, jNul, aE, aE, jEnd), "[\n    [\n        null\n    ]\n]", "json")
}

func TestAnIndentOfZeroIsCompact(t *testing.T) {
	out, _ := compact(doc...)
	eq(t, indented(t, 0, doc...), out, "json")
}

func TestTheTrailingNewlineIsWrittenAtEndWhenAsked(t *testing.T) {
	out, f := renderJSON(JSONOptions{TrailingNewline: true}, evs(jNul, jEnd))
	ok(t, f)
	eq(t, out, "null\n", "json")
	out, _ = compact(jNul, jEnd)
	eq(t, out, "null", "json")
}

func TestARootScalarIsADocument(t *testing.T) {
	for _, c := range []struct {
		ev   tt.Event
		want string
	}{{str("x"), `"x"`}, {jnum("-0.5e3"), "-0.5e3"}, {jb(false), "false"}} {
		out, f := compact(c.ev, jEnd)
		ok(t, f)
		eq(t, out, c.want, "json")
	}
}

func TestStringsAreEscapedAsRFC8259RequiresAndNoMore(t *testing.T) {
	text := "q\" b\\ n\n r\r t\t bs\b ff\f nul\x00 c1\x01 us\x1f del\x7f é 日本 🚀 /"
	out, f := compact(str(text), jEnd)
	ok(t, f)
	eq(t, out, "\"q\\\" b\\\\ n\\n r\\r t\\t bs\\b ff\\f nul\\u0000 c1\\u0001 us\\u001f del\x7f é 日本 🚀 /\"", "json")
	out, f = compact(oS, key("k\"\n"), jNul, oE, jEnd)
	ok(t, f)
	eq(t, out, "{\"k\\\"\\n\":null}", "json")
}

// fragments is a TextOut that keeps every fragment apart, so streaming is
// observable.
type fragments struct{ parts []string }

func (f *fragments) WriteStr(s string) *tt.Fail { f.parts = append(f.parts, s); return nil }
func (f *fragments) Flush() *tt.Fail            { return nil }
func (f *fragments) HasCommitted() bool         { return true }

func TestStringsEscapeExactlyAsTransduceDoes(t *testing.T) {
	var everyControl strings.Builder
	for c := 0; c < 0x20; c++ {
		everyControl.WriteByte(byte(c))
		everyControl.WriteByte('x')
	}
	for _, text := range []string{
		"", "plain", `"`, `\`, `"\"\`, "a\"b\\c\nd", everyControl.String(),
		"é 日本 🚀 \u007f \u0080 \u2028 \uffff", "ends with control \x01", "\x01 starts with control",
	} {
		var want strings.Builder
		tt.WriteJSONString(text, &want)
		out, f := compact(str(text), jEnd)
		ok(t, f)
		eq(t, out, want.String(), fmt.Sprintf("%q", text))
	}
}

func TestStringsAreStreamedInRunsAndNeverCopiedWhole(t *testing.T) {
	frag := &fragments{}
	r := NewJSONRenderer(frag, JSONOptions{})
	_, f := r.Event(str("ab\"cd\n\x01ef"))
	ok(t, f)
	eq(t, fmt.Sprintf("%q", frag.parts), fmt.Sprintf("%q", []string{`"`, "ab", `\"`, "cd", `\n`, `\u0001`, "ef", `"`}), "fragments")
	// A long string of control characters, six bytes of output each,
	// leaves nothing behind in the renderer.
	big := strings.Repeat("\x01", 64*1024)
	r2 := NewJSONRenderer(NewStringOut(), JSONOptions{})
	_, f = r2.Event(str(big))
	ok(t, f)
	eq(t, r2.Inner().Len(), len(big)*6+2, "length")
	eq(t, cap(r2.scratch), 0, "strings do not touch the scratch")
}

func TestNumbersKeepTheirLexemeOrTakeTheShortestForm(t *testing.T) {
	out, f := compact(aS, jnum("1.00"), jnum("123456789012345678901234567890"), jnum("-0"), jnum("1E+2"),
		jv(0), jv(1e21), jv(0.1), jv(-2), aE, jEnd)
	ok(t, f)
	eq(t, out, "[1.00,123456789012345678901234567890,-0,1E+2,0,1e21,0.1,-2]", "json")
	out, f = compact(aS, jv(1e300), jv(1e-300), jv(1.5e17), jv(1e20), aE, jEnd)
	ok(t, f)
	eq(t, out, "[1e300,1e-300,150000000000000000,100000000000000000000]", "json")
}

func TestJSONALexemeThatIsNotAJSONNumberIsInvalidNumber(t *testing.T) {
	for _, bad := range []string{"1.", "01", "NaN", "+1", "0x1"} {
		_, f := compact(aS, jnum(bad), aE, jEnd)
		code(t, f, tt.CodeInvalidNumber)
		eq(t, f.CommittedOutput, true, "committed: "+bad)
	}
	_, f := compact(jnum("1."), jEnd)
	eq(t, f.CommittedOutput, false, "committed")
}

func TestNaNAndInfinityAreUnrepresentable(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, f := compact(jv(v), jEnd)
		code(t, f, tt.CodeTargetValueUnrepresentable)
	}
}

func TestAnOverflowedLexemeIsUnrepresentableToo(t *testing.T) {
	overflowed := tt.EvNumberLexeme(math.Inf(1), "1e999")
	r := NewJSONRenderer(NewStringOut(), JSONOptions{})
	_, f := r.Event(aS)
	ok(t, f)
	_, f = r.Event(jnum("1"))
	ok(t, f)
	_, f = r.Event(overflowed)
	code(t, f, tt.CodeTargetValueUnrepresentable)
	eq(t, f.CommittedOutput, true, "committed")
	eq(t, r.Inner().String(), "[1", "text")
	_, f = compact(overflowed, jEnd)
	code(t, f, tt.CodeTargetValueUnrepresentable)
	eq(t, f.CommittedOutput, false, "committed")
}

func TestARejectedNumberLeavesNoSeparatorBehind(t *testing.T) {
	r := NewJSONRenderer(NewStringOut(), JSONOptions{})
	for _, ev := range evs(aS, jnum("1")) {
		_, f := r.Event(ev)
		ok(t, f)
	}
	_, f := r.Event(jnum("01"))
	code(t, f, tt.CodeInvalidNumber)
	eq(t, r.Inner().String(), "[1", "text")
	_, f = r.Event(jv(math.NaN()))
	code(t, f, tt.CodeTargetValueUnrepresentable)
	eq(t, r.Inner().String(), "[1", "text")
	// A caller that carries on regardless still gets a document.
	for _, ev := range evs(jv(2), aE, jEnd) {
		_, f := r.Event(ev)
		ok(t, f)
	}
	eq(t, r.Inner().String(), "[1,2]", "text")
}

func protocolError(t *testing.T, events ...tt.Event) *tt.Fail {
	t.Helper()
	_, f := compact(events...)
	if f == nil || f.Code != tt.CodeProtocolOrderError {
		t.Fatalf("%v: got %v, want PROTOCOL_ORDER_ERROR", events, f)
	}
	return f
}

func TestASecondRootIsAProtocolError(t *testing.T) {
	protocolError(t, jNul, jNul)
	protocolError(t, oS, oE, aS)
	protocolError(t, str("a"), str("b"), jEnd)
}

func TestAnEndWithoutACompleteRootIsAProtocolError(t *testing.T) {
	eq(t, protocolError(t, jEnd).CommittedOutput, false, "committed")
	protocolError(t, aS, jEnd)
	protocolError(t, oS, key("a"), jEnd)
	protocolError(t, oS, key("a"), jNul, jEnd)
}

func TestAKeyOutsideAnObjectOrWhereAValueIsDueIsAProtocolError(t *testing.T) {
	protocolError(t, key("a"))
	protocolError(t, aS, key("a"))
	protocolError(t, oS, key("a"), key("b"))
	protocolError(t, jNul, key("a"))
}

func TestAValueWhereAKeyIsDueIsAProtocolError(t *testing.T) {
	protocolError(t, oS, jNul)
	protocolError(t, oS, aS)
	protocolError(t, oS, key("a"), jNul, str("b"))
}

func TestAnUnbalancedOrMismatchedCloseIsAProtocolError(t *testing.T) {
	protocolError(t, oE)
	protocolError(t, aE)
	protocolError(t, aS, oE)
	protocolError(t, oS, aE)
	protocolError(t, oS, key("a"), oE)
	protocolError(t, aS, aE, aE)
}

func TestAnythingAfterTheEndIsAProtocolError(t *testing.T) {
	for _, after := range evs(jEnd, jNul, key("a"), oS, oE, aE) {
		eq(t, protocolError(t, jNul, jEnd, after).CommittedOutput, true, "committed")
	}
}

func TestAFailureLeavesTheOutputAPrefixOfTheDocument(t *testing.T) {
	r := NewJSONRenderer(NewStringOut(), JSONOptions{})
	for _, ev := range evs(oS, key("a"), aS, jNul) {
		_, f := r.Event(ev)
		ok(t, f)
	}
	eq(t, r.Depth(), 2, "depth")
	_, f := r.Event(key("b"))
	code(t, f, tt.CodeProtocolOrderError)
	eq(t, r.Inner().String(), `{"a":[null`, "text")
}

func TestJSONAFailedFlushAtEndLeavesTheRendererNotDone(t *testing.T) {
	r := NewJSONRenderer(NewWriteOut(noFlush{}), JSONOptions{})
	_, f := r.Event(jNul)
	ok(t, f)
	_, f = r.Event(jEnd)
	code(t, f, tt.CodeOutputFailed)
	eq(t, r.IsDone(), false, "done")
}

func TestJSONCommittedOutputMeansBytesThatReachedTheWriter(t *testing.T) {
	r := NewJSONRenderer(NewWriteOut(&bytes.Buffer{}), JSONOptions{})
	_, f := r.Event(aS)
	ok(t, f)
	_, f = r.Event(key("k"))
	code(t, f, tt.CodeProtocolOrderError)
	eq(t, f.CommittedOutput, false, "the bracket is only buffered")
	eq(t, r.Inner().Committed(), 0, "committed bytes")

	r = NewJSONRenderer(NewWriteOut(&bytes.Buffer{}).WithBudget(0), JSONOptions{})
	_, f = r.Event(aS)
	ok(t, f)
	_, f = r.Event(key("k"))
	eq(t, f.CommittedOutput, true, "the bracket reached the writer")
}

func TestJSONEndFlushesTheOutputAndNothingElseDoes(t *testing.T) {
	var b bytes.Buffer
	r := NewJSONRenderer(NewWriteOut(&b), JSONOptions{})
	for _, ev := range evs(aS, jb(true), aE) {
		flow, f := r.Event(ev)
		ok(t, f)
		eq(t, flow, tt.Continue, "flow")
	}
	eq(t, r.Inner().Committed(), 0, "committed")
	eq(t, r.IsDone(), false, "done")
	_, f := r.Event(jEnd)
	ok(t, f)
	eq(t, r.IsDone(), true, "done")
	eq(t, r.Inner().Committed(), 6, "committed")
	eq(t, b.String(), "[true]", "text")
}
