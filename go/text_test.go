// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// The chunk-boundary tests of rs/src/text.rs: coalescing at the budget,
// the limit failing before the write, the short-write accounting, joins
// with empty items, replacements split at every byte of the literal.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	tt "github.com/tabnas/transduce/go"
)

// chunks is a writer that records each Write as one chunk, so coalescing
// is observable, and fails after a set number of bytes when asked. It
// takes a buffer whole or refuses it whole.
type chunks struct {
	chunks    []string
	flushes   int
	failAfter int // -1 for never
}

func newChunks() *chunks { return &chunks{failAfter: -1} }

func (c *chunks) Write(p []byte) (int, error) {
	soFar := 0
	for _, ch := range c.chunks {
		soFar += len(ch)
	}
	if c.failAfter >= 0 && soFar+len(p) > c.failAfter {
		return 0, errors.New("disk full")
	}
	c.chunks = append(c.chunks, string(p))
	return len(p), nil
}

func (c *chunks) Flush() error {
	c.flushes++
	return nil
}

func (c *chunks) joined() string { return strings.Join(c.chunks, "") }

func eq[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func ok(t *testing.T, f *tt.Fail) {
	t.Helper()
	if f != nil {
		t.Fatalf("unexpected failure: %v", f)
	}
}

func code(t *testing.T, f *tt.Fail, want tt.Code) *tt.Fail {
	t.Helper()
	if f == nil {
		t.Fatalf("no failure, want %s", want)
	}
	if f.Code != want {
		t.Fatalf("failure %v, want %s", f, want)
	}
	return f
}

func limitOf(n uint64) tt.Limits {
	l := tt.DefaultLimits()
	l.MaxOutputBytes = &n
	return l
}

func TestFragmentsCoalesceUpToTheBudgetAndTheBufferNeverExceedsIt(t *testing.T) {
	w := newChunks()
	out := NewWriteOut(w).WithBudget(8)
	ok(t, out.WriteStr("abc"))
	ok(t, out.WriteStr("def"))
	ok(t, out.WriteStr("gh"))
	// Exactly the budget: still held.
	eq(t, len(w.chunks), 0, "chunks at the budget")
	ok(t, out.WriteStr("i"))
	eq(t, fmt.Sprint(w.chunks), "[abcdefgh]", "chunks")
	eq(t, out.Committed(), 8, "committed")
	eq(t, out.Accepted(), 9, "accepted")
	// A fragment at least as large as the budget bypasses the buffer,
	// after what was buffered before it.
	ok(t, out.WriteStr("0123456789"))
	eq(t, fmt.Sprint(w.chunks), "[abcdefgh i 0123456789]", "chunks")
	ok(t, out.WriteStr("z"))
	ok(t, out.Flush())
	eq(t, w.joined(), "abcdefghi0123456789z", "text")
	eq(t, w.flushes, 1, "flushes")
}

func TestAZeroBudgetWritesEveryFragmentAsItArrives(t *testing.T) {
	w := newChunks()
	out := NewWriteOut(w).WithBudget(0)
	ok(t, out.WriteStr("a"))
	ok(t, out.WriteStr(""))
	ok(t, out.WriteStr("bc"))
	eq(t, fmt.Sprint(w.chunks), "[a bc]", "chunks: a zero-length fragment is never a write")
}

func TestTheOutputLimitFailsBeforeTheFragmentThatWouldExceedIt(t *testing.T) {
	w := newChunks()
	metrics := tt.NewMetrics()
	out := NewWriteOut(w).WithBudget(4).WithLimits(limitOf(10)).WithMetrics(metrics)
	ok(t, out.WriteStr("hello"))
	ok(t, out.WriteStr("worl"))
	f := code(t, out.WriteStr("d!"), tt.CodeResourceLimitExceeded)
	if f.Limit == nil || f.Limit.Name != "max_output_bytes" || f.Limit.Value != 10 {
		t.Errorf("limit %+v", f.Limit)
	}
	// "hello" crossed the budget when "worl" arrived, so it was written
	// before the failure and the failure says so.
	eq(t, f.CommittedOutput, true, "committed output")
	eq(t, out.Accepted(), 9, "accepted")
	eq(t, out.Committed(), 9, "committed")
	eq(t, w.joined(), "helloworl", "text")
	eq(t, metrics.OutputBytes.Load(), 9, "output bytes")
}

func TestInnerAfterAFailureHandsBackExactlyTheCommittedBytes(t *testing.T) {
	w := newChunks()
	out := NewWriteOut(w).WithBudget(100).WithLimits(limitOf(5))
	ok(t, out.WriteStr("abc"))
	f := code(t, out.WriteStr("xyz"), tt.CodeResourceLimitExceeded)
	eq(t, f.CommittedOutput, false, "committed output")
	eq(t, out.Committed(), 0, "committed")
	eq(t, out.Inner().(*chunks), w, "inner")
	eq(t, len(w.chunks), 0, "no committed output means none")
	eq(t, w.flushes, 0, "flushes")
}

func TestACallerThatWantsThePartialOutputFlushesBeforeInner(t *testing.T) {
	w := newChunks()
	out := NewWriteOut(w).WithBudget(100)
	ok(t, out.WriteStr("abc"))
	ok(t, out.Flush())
	eq(t, w.joined(), "abc", "text")
	eq(t, w.flushes, 1, "flushes")
}

func TestALimitFailureWithNothingWrittenIsNotCommitted(t *testing.T) {
	w := newChunks()
	out := NewWriteOut(w).WithLimits(limitOf(3))
	f := code(t, out.WriteStr("abcd"), tt.CodeResourceLimitExceeded)
	eq(t, f.CommittedOutput, false, "committed output")
	eq(t, len(w.chunks), 0, "chunks")
}

func TestTheLimitCountsUTF8Bytes(t *testing.T) {
	out := NewWriteOut(newChunks()).WithLimits(limitOf(3))
	code(t, out.WriteStr("🚀"), tt.CodeResourceLimitExceeded)
	out = NewWriteOut(newChunks()).WithLimits(limitOf(4))
	ok(t, out.WriteStr("🚀"))
	eq(t, out.Accepted(), 4, "accepted")
}

func TestAnIOErrorIsOutputFailedAndSaysWhetherBytesWereCommitted(t *testing.T) {
	w := newChunks()
	w.failAfter = 4
	out := NewWriteOut(w).WithBudget(3)
	ok(t, out.WriteStr("abc"))
	eq(t, out.Committed(), 3, "committed")
	ok(t, out.WriteStr("de"))
	eq(t, out.Committed(), 3, "committed")
	f := code(t, out.Flush(), tt.CodeOutputFailed)
	eq(t, f.CommittedOutput, true, "committed output")
	eq(t, strings.Contains(f.Message, "disk full"), true, "message names the cause")

	w = newChunks()
	w.failAfter = 0
	out = NewWriteOut(w).WithBudget(0)
	f = code(t, out.WriteStr("x"), tt.CodeOutputFailed)
	eq(t, f.CommittedOutput, false, "committed output")
}

// cramped is a writer with room for room bytes that takes what fits of
// each write and reports the error with the short count, as io.Writer
// requires: the short write before "no space left on device".
type cramped struct {
	room  int
	taken []byte
}

func (c *cramped) Write(p []byte) (int, error) {
	left := c.room - len(c.taken)
	n := len(p)
	if n > left {
		n = left
	}
	c.taken = append(c.taken, p[:n]...)
	if n < len(p) {
		return n, errors.New("disk full")
	}
	return n, nil
}

func TestAShortWriteBeforeTheFailureCountsTheBytesTheWriterTook(t *testing.T) {
	// Buffered, then flushed: the writer takes three bytes of the six and
	// fails on the rest.
	metrics := tt.NewMetrics()
	w := &cramped{room: 3}
	out := NewWriteOut(w).WithBudget(100).WithMetrics(metrics)
	ok(t, out.WriteStr("abc"))
	ok(t, out.WriteStr("def"))
	eq(t, out.Committed(), 0, "still buffered")
	f := code(t, out.Flush(), tt.CodeOutputFailed)
	eq(t, strings.Contains(f.Message, "disk full"), true, "message")
	eq(t, f.CommittedOutput, true, "three bytes reached the writer before it failed")
	eq(t, out.HasCommitted(), true, "has committed")
	eq(t, out.Committed(), 3, "committed")
	eq(t, out.Accepted(), 6, "accepted")
	eq(t, metrics.OutputBytes.Load(), 3, "output bytes")
	eq(t, string(w.taken), "abc", "the writer holds exactly Committed() bytes")

	// Written directly: a fragment as large as the budget takes the same
	// path and is counted the same way.
	w = &cramped{room: 2}
	out = NewWriteOut(w).WithBudget(0)
	f = code(t, out.WriteStr("abcdef"), tt.CodeOutputFailed)
	eq(t, f.CommittedOutput, true, "committed output")
	eq(t, out.Committed(), 2, "committed")
	eq(t, out.Accepted(), 0, "the fragment was not accepted")
	eq(t, string(w.taken), "ab", "taken")

	// No room at all: nothing was taken, and the failure says so.
	w = &cramped{room: 0}
	out = NewWriteOut(w).WithBudget(0)
	f = code(t, out.WriteStr("abc"), tt.CodeOutputFailed)
	eq(t, f.CommittedOutput, false, "committed output")
	eq(t, out.HasCommitted(), false, "has committed")
	eq(t, len(w.taken), 0, "taken")
}

// zero accepts nothing and reports no error, which io.Writer forbids and
// a renderer must still survive.
type zero struct{}

func (zero) Write([]byte) (int, error) { return 0, nil }

func TestAWriterThatTakesNothingIsWriteZeroNotASpin(t *testing.T) {
	out := NewWriteOut(zero{}).WithBudget(0)
	f := code(t, out.WriteStr("abc"), tt.CodeOutputFailed)
	eq(t, strings.Contains(f.Message, "failed to write whole buffer"), true, "message")
	eq(t, f.CommittedOutput, false, "committed output")
	eq(t, out.Committed(), 0, "committed")
}

// halves takes half of each write (at least a byte) and reports no
// error, which io.Writer forbids: the loop carries on with the rest, as
// Rust's write_all does after a short write.
type halves struct{ taken []byte }

func (h *halves) Write(p []byte) (int, error) {
	n := (len(p) + 1) / 2
	h.taken = append(h.taken, p[:n]...)
	return n, nil
}

func TestAShortWriteWithoutAnErrorIsContinuedAndCountedOnce(t *testing.T) {
	w := &halves{}
	out := NewWriteOut(w).WithBudget(4)
	ok(t, out.WriteStr("abcd"))
	ok(t, out.WriteStr("efgh"))
	ok(t, out.Flush())
	eq(t, out.Committed(), 8, "committed")
	eq(t, string(w.taken), "abcdefgh", "taken")
}

// shortWriteChild marks the process running under the file-size limit in
// the test below.
const shortWriteChild = "TABNAS_RENDER_SHORT_WRITE_CHILD"

// runShortWriteChild is the half of the test that runs under the limit:
// a real file, a buffer larger than the limit, one flush. It reports on
// standard output with a `short-write:` line, which the parent reads.
func runShortWriteChild(t *testing.T) {
	fmt.Println("short-write: start")
	path := fmt.Sprintf("%s/tabnas-render-short-write-%d.txt", os.TempDir(), os.Getpid())
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	metrics := tt.NewMetrics()
	out := NewWriteOut(file).WithMetrics(metrics)
	text := strings.Repeat("0123456789abcdef", 1000)
	ok(t, out.WriteStr(text))
	eq(t, out.Committed(), 0, "under the default budget it is buffered")
	f := out.Flush()
	committed := out.Committed()
	info, statErr := file.Stat()
	file.Close()
	if statErr != nil {
		t.Fatal(statErr)
	}
	onDisk := uint64(info.Size())
	switch {
	case f == nil:
		fmt.Printf("short-write: skipped, no file-size limit was in force (%d bytes on disk)\n", onDisk)
	case committed == 0 && onDisk == 0:
		fmt.Printf("short-write: skipped, the limit refused the write whole: %s\n", f.Message)
	default:
		fmt.Printf("short-write: ran committed=%d on_disk=%d output_bytes=%d code=%s committed_output=%v\n",
			committed, onDisk, metrics.OutputBytes.Load(), f.Code, f.CommittedOutput)
		eq(t, f.Code, tt.CodeOutputFailed, "code")
		eq(t, f.CommittedOutput, true, "the kernel took part of the buffer, so output is partial")
		eq(t, out.HasCommitted(), true, "has committed")
		eq(t, committed, onDisk, "Committed() must be what the file holds")
		eq(t, metrics.OutputBytes.Load(), committed, "output bytes")
		eq(t, committed < uint64(len(text)), true, "the limit cut the write")
	}
}

// TestAFileUnderASizeLimitHoldsExactlyTheCommittedBytes is the short write
// on a real file: the kernel accepts the bytes up to the limit and
// refuses the rest, and the file on disk holds exactly Committed() bytes.
// RLIMIT_FSIZE is the limit that needs no privileges; the shell's `ulimit
// -f` sets it per process, so the run happens in a child (this test
// binary, this test), and the shell ignores SIGXFSZ first so that the
// write past the limit fails with EFBIG instead of ending the child.
// Where no shell can impose the limit, the test says so and passes.
func TestAFileUnderASizeLimitHoldsExactlyTheCommittedBytes(t *testing.T) {
	if os.Getenv(shortWriteChild) != "" {
		runShortWriteChild(t)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("skipped: no test binary path (%v)", err)
	}
	cmd := exec.Command("sh", "-c", `trap "" XFSZ && ulimit -f 8 && exec "$0" "$@"`, exe,
		"-test.run", "^TestAFileUnderASizeLimitHoldsExactlyTheCommittedBytes$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), shortWriteChild+"=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var report string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "short-write: ") {
			report = line
		}
	}
	switch {
	case strings.HasPrefix(report, "short-write: ran"):
		t.Log(report)
		if runErr != nil {
			t.Fatalf("the run under the limit failed: %s\n%s\n%s", report, stdout.String(), stderr.String())
		}
	case strings.HasPrefix(report, "short-write: skipped"):
		t.Log(report)
	case report != "":
		t.Fatalf("the child stopped after `%s` (%v):\n%s\n%s", report, runErr, stdout.String(), stderr.String())
	default:
		t.Logf("skipped: the shell could not impose a file-size limit (%v): %s", runErr, strings.TrimSpace(stderr.String()))
	}
}

func TestMetricsCountBytesHandedToTheWriter(t *testing.T) {
	metrics := tt.NewMetrics()
	var b bytes.Buffer
	out := NewWriteOut(&b).WithBudget(100).WithMetrics(metrics)
	ok(t, out.WriteStr("twelve bytes"))
	eq(t, metrics.OutputBytes.Load(), 0, "buffered")
	ok(t, out.Flush())
	eq(t, metrics.OutputBytes.Load(), 12, "written")
	eq(t, b.String(), "twelve bytes", "text")
}

func TestHasCommittedIsAnsweredByTheDestinationNotTheBuffer(t *testing.T) {
	out := NewWriteOut(newChunks()).WithBudget(100)
	eq(t, out.HasCommitted(), false, "fresh")
	ok(t, out.WriteStr("abc"))
	eq(t, out.HasCommitted(), false, "buffered is not committed")
	ok(t, out.Flush())
	eq(t, out.HasCommitted(), true, "flushed")

	s := NewStringOut()
	eq(t, s.HasCommitted(), false, "empty string")
	ok(t, s.WriteStr("x"))
	eq(t, s.HasCommitted(), true, "the string is the destination")

	// The combinators forward the question; a replacer's carry has gone
	// nowhere yet.
	inner := NewWriteOut(&bytes.Buffer{}).WithBudget(100)
	r := NewReplaceText(NewJoin(inner, ","), "ab", "")
	ok(t, r.WriteStr("xa"))
	eq(t, r.HasCommitted(), false, "carried")
	ok(t, r.Flush())
	eq(t, r.HasCommitted(), true, "flushed")
	var asOut TextOut = r
	eq(t, asOut.HasCommitted(), true, "through the interface")
}

func TestStringOutKeepsTheText(t *testing.T) {
	s := NewStringOut()
	ok(t, s.WriteStr("a"))
	ok(t, s.WriteStr("b"))
	ok(t, s.Flush())
	eq(t, s.String(), "ab", "text")
	eq(t, s.Len(), 2, "len")
}

func TestJoinSeparatesItemsNotFragments(t *testing.T) {
	j := NewJoin(NewStringOut(), ", ")
	ok(t, j.ItemStart())
	ok(t, j.WriteStr("a"))
	ok(t, j.WriteStr("b"))
	ok(t, j.ItemEnd())
	ok(t, j.ItemStart())
	ok(t, j.WriteStr("c"))
	ok(t, j.ItemEnd())
	eq(t, j.Items(), 2, "items")
	eq(t, j.Inner().String(), "ab, c", "text")
}

func TestJoinCountsEmptyItems(t *testing.T) {
	j := NewJoin(NewStringOut(), ",")
	for i := 0; i < 3; i++ {
		ok(t, j.ItemStart())
		ok(t, j.ItemEnd())
	}
	ok(t, j.ItemStart())
	ok(t, j.WriteStr("x"))
	ok(t, j.ItemEnd())
	ok(t, j.ItemStart())
	ok(t, j.ItemEnd())
	eq(t, j.Inner().String(), ",,,x,", "text")
}

func TestJoinTreatsAFragmentOutsideAnItemAsAnItem(t *testing.T) {
	j := NewJoin(NewStringOut(), "|")
	ok(t, j.WriteStr("a"))
	ok(t, j.WriteStr(""))
	ok(t, j.WriteStr("b"))
	ok(t, j.Flush())
	eq(t, j.Inner().String(), "a||b", "text")
}

func TestJoinWithNoItemsWritesNothing(t *testing.T) {
	j := NewJoin(NewStringOut(), ",")
	ok(t, j.Flush())
	eq(t, j.Inner().String(), "", "text")
}

func TestJoinRejectsUnbalancedItemMarkers(t *testing.T) {
	j := NewJoin(NewStringOut(), ",")
	code(t, j.ItemEnd(), tt.CodeProtocolOrderError)
	ok(t, j.ItemStart())
	code(t, j.ItemStart(), tt.CodeProtocolOrderError)
}

func TestConcatAppendsItemsAndFragmentsWithNothingBetweenThem(t *testing.T) {
	c := NewConcat(NewStringOut())
	ok(t, c.ItemStart())
	ok(t, c.WriteStr("a"))
	ok(t, c.WriteStr("b"))
	ok(t, c.ItemEnd())
	ok(t, c.ItemStart())
	ok(t, c.ItemEnd())
	ok(t, c.WriteStr("c"))
	ok(t, c.Flush())
	eq(t, c.Items(), 3, "items")
	eq(t, c.HasCommitted(), true, "has committed")
	eq(t, c.Inner().String(), "abc", "text")
}

func TestConcatKeepsJoinsItemDiscipline(t *testing.T) {
	var b bytes.Buffer
	c := NewConcat(NewWriteOut(&b))
	code(t, c.ItemEnd(), tt.CodeProtocolOrderError)
	ok(t, c.ItemStart())
	code(t, c.ItemStart(), tt.CodeProtocolOrderError)
	ok(t, c.WriteStr("x"))
	eq(t, c.HasCommitted(), false, "buffered beneath, not yet written")
	ok(t, c.ItemEnd())
	ok(t, c.Flush())
	eq(t, b.String(), "x", "text")
}

// replacedSplit feeds text to a replacer split at at, then flushed.
func replacedSplit(t *testing.T, text string, at int, from, to string) string {
	r := NewReplaceText(NewStringOut(), from, to)
	ok(t, r.WriteStr(text[:at]))
	ok(t, r.WriteStr(text[at:]))
	ok(t, r.Flush())
	return r.Inner().String()
}

func TestReplaceMatchesReplaceAllWhenSplitAtEveryBoundary(t *testing.T) {
	cases := [][3]string{
		{"abcabc", "abc", "X"},
		{"xxabcxxabcxx", "abc", ""},
		{"aaaa", "aa", "b"},
		{"aaaaa", "aa", "b"},
		{"ababab", "aba", "_"},
		{"no match here", "zzz", "Y"},
		{"abab", "abab", "1"},
		{"ab", "abc", "1"},
		{"héllo wörld héllo", "héllo", "hi"},
		{"日本語日本", "日本", "*"},
		{"a\r\nb\r\n", "\r\n", "\n"},
	}
	for _, c := range cases {
		text, from, to := c[0], c[1], c[2]
		want := strings.ReplaceAll(text, from, to)
		for at := 0; at <= len(text); at++ {
			if at < len(text) && (text[at]&0xC0) == 0x80 {
				continue // not a character boundary
			}
			if got := replacedSplit(t, text, at, from, to); got != want {
				t.Errorf("%q split at %d replacing %q: got %q, want %q", text, at, from, got, want)
			}
		}
	}
}

func TestReplaceAcrossManyOneCharacterFragments(t *testing.T) {
	text := "the cat sat on the mat with the hat"
	r := NewReplaceText(NewStringOut(), "the", "a")
	for _, c := range text {
		ok(t, r.WriteStr(string(c)))
	}
	ok(t, r.Flush())
	eq(t, r.Inner().String(), strings.ReplaceAll(text, "the", "a"), "text")
}

func TestReplaceNeverCarriesMoreThanTheLiteralLessOneByte(t *testing.T) {
	r := NewReplaceText(NewStringOut(), "abcd", "")
	ok(t, r.WriteStr("xxabc"))
	eq(t, r.carry, "abc", "carry")
	eq(t, r.out.String(), "xx", "text")
	ok(t, r.WriteStr("ab"))
	eq(t, r.carry, "ab", "carry")
	eq(t, r.out.String(), "xxabc", "text")
	ok(t, r.WriteStr("cdab"))
	eq(t, r.carry, "ab", "carry")
	eq(t, r.out.String(), "xxabc", "text")
	ok(t, r.Flush())
	eq(t, r.carry, "", "carry")
	eq(t, r.Inner().String(), "xxabcab", "text")
}

func TestReplaceWithAnEmptyLiteralPassesTextThrough(t *testing.T) {
	r := NewReplaceText(NewStringOut(), "", "X")
	ok(t, r.WriteStr("abc"))
	ok(t, r.Flush())
	eq(t, r.Inner().String(), "abc", "text")
}

func TestReplaceFlushesTheCarryAtFlushSoALaterMatchCannotSpanIt(t *testing.T) {
	r := NewReplaceText(NewStringOut(), "ab", "X")
	ok(t, r.WriteStr("a"))
	ok(t, r.Flush())
	ok(t, r.WriteStr("b"))
	ok(t, r.Flush())
	eq(t, r.Inner().String(), "ab", "text")
}

func TestCombinatorsStackOverAWriter(t *testing.T) {
	var b bytes.Buffer
	inner := NewWriteOut(&b).WithBudget(3)
	j := NewJoin(NewReplaceText(inner, "-", "+"), ";")
	ok(t, j.WriteStr("a-b"))
	ok(t, j.WriteStr("c-"))
	ok(t, j.Flush())
	eq(t, b.String(), "a+b;c+", "text")
}
