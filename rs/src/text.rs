//! Text output: where rendered fragments go.
//!
//! A renderer produces many small fragments (a quote, a field, a comma) and
//! must never hold a whole document. [`TextOut`] is the boundary: a
//! fragment in, a failure out. [`WriteOut`] coalesces fragments to a byte
//! budget before they reach an [`io::Write`], so a renderer can write a
//! character at a time without paying a system call for each, and it is
//! where the output limit and the output-bytes metric live, because it is
//! the one stage that knows what actually left. [`Join`] and
//! [`ReplaceText`] are the two text combinators whose correctness depends
//! on the difference between a logical item and a transport chunk, which is
//! why they live beside the writer rather than in the language that uses
//! them.

use std::io;
use std::sync::Arc;

use tabnas_transduce::{Fail, Limits, Metrics};

/// A consumer of text fragments.
///
/// Fragments arrive in order and are concatenated; where the boundaries
/// fall carries no meaning. `flush` pushes everything held so far to the
/// final destination, and a renderer calls it exactly once, at the end of
/// the protocol it renders, so that a document that failed half way is not
/// flushed as if it were whole.
pub trait TextOut {
    fn write_str(&mut self, s: &str) -> Result<(), Fail>;
    fn flush(&mut self) -> Result<(), Fail>;
}

impl<O: TextOut + ?Sized> TextOut for &mut O {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        (**self).write_str(s)
    }

    fn flush(&mut self) -> Result<(), Fail> {
        (**self).flush()
    }
}

impl<O: TextOut + ?Sized> TextOut for Box<O> {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        (**self).write_str(s)
    }

    fn flush(&mut self) -> Result<(), Fail> {
        (**self).flush()
    }
}

/// The default coalescing budget of a [`WriteOut`]: large enough that a
/// write per budget is negligible next to the parse, small enough to be
/// invisible in a process's memory.
pub const DEFAULT_BUDGET: usize = 32 * 1024;

/// Coalesces fragments and writes them to an [`io::Write`].
///
/// Retention is bounded by the budget: the buffer never holds more than
/// `budget` bytes, and a fragment at least as large as the budget goes to
/// the writer directly, after whatever was buffered before it. The output
/// limit is checked on every fragment BEFORE it is accepted, counting the
/// bytes buffered as well as the bytes written, so a run that would exceed
/// `max_output_bytes` fails without emitting the fragment that crossed the
/// line. `output_bytes` in the shared [`Metrics`] counts bytes handed to
/// the writer, which is what "written to the output" means to a caller
/// reading the metrics after a failure.
pub struct WriteOut<W: io::Write> {
    writer: W,
    buf: Vec<u8>,
    budget: usize,
    max_output_bytes: Option<u64>,
    metrics: Option<Arc<Metrics>>,
    /// Bytes accepted: buffered or written.
    accepted: u64,
    /// Bytes handed to the writer. Nonzero means a later failure finds
    /// committed output.
    committed: u64,
}

impl<W: io::Write> WriteOut<W> {
    pub fn new(writer: W) -> Self {
        WriteOut {
            writer,
            buf: Vec::new(),
            budget: DEFAULT_BUDGET,
            max_output_bytes: None,
            metrics: None,
            accepted: 0,
            committed: 0,
        }
    }

    /// The coalescing budget in bytes. Zero means every fragment is written
    /// as it arrives, which is what a test of the writer's ordering wants.
    pub fn with_budget(mut self, budget: usize) -> Self {
        self.budget = budget;
        self
    }

    /// Enforce `limits.max_output_bytes`; the other limits belong to the
    /// stages upstream.
    pub fn with_limits(mut self, limits: &Limits) -> Self {
        self.max_output_bytes = limits.max_output_bytes;
        self
    }

    /// Count `output_bytes` into these metrics.
    pub fn with_metrics(mut self, metrics: Arc<Metrics>) -> Self {
        self.metrics = Some(metrics);
        self
    }

    /// Bytes accepted so far, buffered or written.
    pub fn accepted(&self) -> u64 {
        self.accepted
    }

    /// Bytes that reached the writer.
    pub fn committed(&self) -> u64 {
        self.committed
    }

    /// Hand the writer back WITHOUT flushing. Whatever the buffer still
    /// holds is dropped, so the writer holds exactly the bytes `committed()`
    /// counts: a document that failed before its `End` does not reach the
    /// writer on the way out, which is what a failure that reported no
    /// committed output promised the host. A renderer flushes once, at its
    /// `End`, and a caller that wants a partial output anyway calls `flush`
    /// first, knowingly.
    pub fn into_inner(self) -> W {
        self.writer
    }

    fn fail_io(&self, e: io::Error) -> Fail {
        let f = Fail::output(format!("writing the output failed: {e}"));
        if self.committed > 0 {
            f.committed()
        } else {
            f
        }
    }

    fn send(&mut self, bytes: &[u8]) -> Result<(), Fail> {
        if let Err(e) = self.writer.write_all(bytes) {
            // The buffer is not retried: a failed writer is done, and the
            // caller learns how many bytes it received before that.
            self.buf.clear();
            return Err(self.fail_io(e));
        }
        self.committed += bytes.len() as u64;
        if let Some(m) = &self.metrics {
            Metrics::add(&m.output_bytes, bytes.len() as u64);
        }
        Ok(())
    }

    fn drain(&mut self) -> Result<(), Fail> {
        if self.buf.is_empty() {
            return Ok(());
        }
        let pending = std::mem::take(&mut self.buf);
        let r = self.send(&pending);
        // Keep the allocation: the buffer refills up to the budget again.
        if r.is_ok() {
            self.buf = pending;
            self.buf.clear();
        }
        r
    }
}

impl<W: io::Write> TextOut for WriteOut<W> {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        let len = s.len() as u64;
        if let Some(max) = self.max_output_bytes {
            if self.accepted.saturating_add(len) > max {
                let f = Fail::limit(
                    "max_output_bytes",
                    max,
                    format!(
                        "the output would exceed {max} bytes: {} written, {len} more",
                        self.accepted
                    ),
                );
                return Err(if self.committed > 0 { f.committed() } else { f });
            }
        }
        if self.buf.len() + s.len() > self.budget {
            self.drain()?;
        }
        if s.len() >= self.budget {
            self.send(s.as_bytes())?;
        } else {
            self.buf.extend_from_slice(s.as_bytes());
        }
        self.accepted += len;
        Ok(())
    }

    fn flush(&mut self) -> Result<(), Fail> {
        self.drain()?;
        self.writer.flush().map_err(|e| self.fail_io(e))
    }
}

/// A [`TextOut`] that keeps the text, for tests and small results.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct StringOut(pub String);

impl StringOut {
    pub fn new() -> Self {
        StringOut::default()
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn into_string(self) -> String {
        self.0
    }
}

impl TextOut for StringOut {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        self.0.push_str(s);
        Ok(())
    }

    fn flush(&mut self) -> Result<(), Fail> {
        Ok(())
    }
}

/// Writes a separator between logical items.
///
/// An item is what lies between [`Join::item_start`] and
/// [`Join::item_end`]; it may be written in any number of fragments, or in
/// none, and an empty item is still an item, so `["", ""]` joined with `,`
/// is `,`. A fragment written outside an item is an item of its own, which
/// is the common case of one text per element and needs no markers. The
/// separator goes before every item but the first, never between the
/// fragments of one item: that distinction is the whole reason this type
/// exists, because a chunked transport must not change the text.
pub struct Join<O: TextOut> {
    out: O,
    separator: Box<str>,
    items: u64,
    in_item: bool,
}

impl<O: TextOut> Join<O> {
    pub fn new(out: O, separator: impl Into<Box<str>>) -> Self {
        Join {
            out,
            separator: separator.into(),
            items: 0,
            in_item: false,
        }
    }

    /// Begin an item: the separator is written now if an item came before.
    /// Starting an item inside an item is `PROTOCOL_ORDER_ERROR`.
    pub fn item_start(&mut self) -> Result<(), Fail> {
        if self.in_item {
            return Err(Fail::protocol(
                "join: an item started inside an item that has not ended",
            ));
        }
        if self.items > 0 && !self.separator.is_empty() {
            self.out.write_str(&self.separator)?;
        }
        self.items += 1;
        self.in_item = true;
        Ok(())
    }

    /// End the current item. Ending when no item is open is
    /// `PROTOCOL_ORDER_ERROR`.
    pub fn item_end(&mut self) -> Result<(), Fail> {
        if !self.in_item {
            return Err(Fail::protocol("join: an item ended when none was open"));
        }
        self.in_item = false;
        Ok(())
    }

    /// Items begun so far.
    pub fn items(&self) -> u64 {
        self.items
    }

    pub fn into_inner(self) -> O {
        self.out
    }
}

impl<O: TextOut> TextOut for Join<O> {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        if self.in_item {
            return self.out.write_str(s);
        }
        self.item_start()?;
        self.out.write_str(s)?;
        self.item_end()
    }

    /// Flushes the output beneath; an open item stays open, since a flush
    /// is about transport and an item is about meaning.
    fn flush(&mut self) -> Result<(), Fail> {
        self.out.flush()
    }
}

/// Replaces every occurrence of a fixed literal, across fragment
/// boundaries.
///
/// The text is treated as one string however it is chunked, with the same
/// left-to-right, non-overlapping matches as `str::replace`. To do that
/// without holding the text, at most `literal.len() - 1` bytes are carried
/// from one fragment to the next: the longest suffix of what has been seen
/// that could still begin a match. `flush` writes the carry out, because
/// nothing can complete it once the caller has declared the text at a
/// boundary; the renderer's single flush at the end of a document is what
/// makes that safe. An empty literal matches nothing and the text passes
/// through unchanged, the one well-defined meaning it can have in a stream.
pub struct ReplaceText<O: TextOut> {
    out: O,
    from: Box<str>,
    to: Box<str>,
    carry: String,
}

impl<O: TextOut> ReplaceText<O> {
    pub fn new(out: O, from: impl Into<Box<str>>, to: impl Into<Box<str>>) -> Self {
        ReplaceText {
            out,
            from: from.into(),
            to: to.into(),
            carry: String::new(),
        }
    }

    pub fn into_inner(self) -> O {
        self.out
    }

    /// The longest proper prefix of the literal that `rest` ends with, in
    /// bytes; zero when there is none. Only char boundaries of the literal
    /// are tried: a match ending inside a character is impossible between
    /// two valid strings, and skipping them keeps every slice below safe.
    fn pending_len(&self, rest: &str) -> usize {
        let max = rest.len().min(self.from.len() - 1);
        (1..=max)
            .rev()
            .find(|&k| self.from.is_char_boundary(k) && rest.ends_with(&self.from[..k]))
            .unwrap_or(0)
    }

    fn scan(&mut self, text: &str) -> Result<(), Fail> {
        let mut rest = text;
        while let Some(i) = rest.find(&*self.from) {
            if i > 0 {
                self.out.write_str(&rest[..i])?;
            }
            if !self.to.is_empty() {
                self.out.write_str(&self.to)?;
            }
            rest = &rest[i + self.from.len()..];
        }
        let keep = self.pending_len(rest);
        // `pending_len` returns the length of a suffix of `rest` that equals a
        // prefix of the literal beginning with a character's first byte, so
        // the split lands on a char boundary; the checked form keeps the
        // guarantee without a panic path.
        match rest.split_at_checked(rest.len() - keep) {
            Some((emit, pending)) => {
                if !emit.is_empty() {
                    self.out.write_str(emit)?;
                }
                self.carry.clear();
                self.carry.push_str(pending);
            }
            None => {
                self.out.write_str(rest)?;
                self.carry.clear();
            }
        }
        Ok(())
    }
}

impl<O: TextOut> TextOut for ReplaceText<O> {
    fn write_str(&mut self, s: &str) -> Result<(), Fail> {
        if self.from.is_empty() {
            return self.out.write_str(s);
        }
        if self.carry.is_empty() {
            self.scan(s)
        } else {
            let mut text = std::mem::take(&mut self.carry);
            text.push_str(s);
            self.scan(&text)
        }
    }

    fn flush(&mut self) -> Result<(), Fail> {
        if !self.carry.is_empty() {
            let pending = std::mem::take(&mut self.carry);
            self.out.write_str(&pending)?;
        }
        self.out.flush()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tabnas_transduce::Code;

    /// A writer that records each `write_all` as one chunk, so coalescing
    /// is observable, and fails after a set number of bytes when asked.
    #[derive(Default, Debug)]
    struct Chunks {
        chunks: Vec<Vec<u8>>,
        flushes: usize,
        fail_after: Option<usize>,
    }

    impl io::Write for Chunks {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            let so_far: usize = self.chunks.iter().map(Vec::len).sum();
            if self.fail_after.is_some_and(|n| so_far + buf.len() > n) {
                return Err(io::Error::other("disk full"));
            }
            self.chunks.push(buf.to_vec());
            Ok(buf.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            self.flushes += 1;
            Ok(())
        }
    }

    fn joined(chunks: &[Vec<u8>]) -> String {
        String::from_utf8(chunks.concat()).unwrap()
    }

    #[test]
    fn fragments_coalesce_up_to_the_budget_and_the_buffer_never_exceeds_it() {
        let mut out = WriteOut::new(Chunks::default()).with_budget(8);
        out.write_str("abc").unwrap();
        out.write_str("def").unwrap();
        out.write_str("gh").unwrap();
        // Exactly the budget: still held.
        assert!(out.writer.chunks.is_empty());
        out.write_str("i").unwrap();
        assert_eq!(out.writer.chunks, vec![b"abcdefgh".to_vec()]);
        assert_eq!(out.committed(), 8);
        assert_eq!(out.accepted(), 9);
        // A fragment at least as large as the budget bypasses the buffer,
        // after what was buffered before it.
        out.write_str("0123456789").unwrap();
        assert_eq!(
            out.writer.chunks,
            vec![b"abcdefgh".to_vec(), b"i".to_vec(), b"0123456789".to_vec()]
        );
        out.write_str("z").unwrap();
        out.flush().unwrap();
        let w = out.into_inner();
        assert_eq!(joined(&w.chunks), "abcdefghi0123456789z");
        assert_eq!(w.flushes, 1);
    }

    #[test]
    fn a_zero_budget_writes_every_fragment_as_it_arrives() {
        let mut out = WriteOut::new(Chunks::default()).with_budget(0);
        out.write_str("a").unwrap();
        out.write_str("bc").unwrap();
        assert_eq!(out.writer.chunks, vec![b"a".to_vec(), b"bc".to_vec()]);
    }

    #[test]
    fn the_output_limit_fails_before_the_fragment_that_would_exceed_it() {
        let limits = Limits {
            max_output_bytes: Some(10),
            ..Limits::default()
        };
        let metrics = Metrics::new();
        let mut out = WriteOut::new(Chunks::default())
            .with_budget(4)
            .with_limits(&limits)
            .with_metrics(Arc::clone(&metrics));
        out.write_str("hello").unwrap();
        out.write_str("worl").unwrap();
        let err = out.write_str("d!").unwrap_err();
        assert_eq!(err.code, Code::ResourceLimitExceeded);
        let limit = err.limit.as_ref().unwrap();
        assert_eq!(limit.name, "max_output_bytes");
        assert_eq!(limit.value, 10);
        // "hello" crossed the budget when "worl" arrived, so it was written
        // before the failure and the failure says so.
        assert!(err.committed_output);
        assert_eq!(out.accepted(), 9);
        // "worl" is as large as the budget, so it went to the writer too;
        // the writer holds what `committed()` says and nothing more.
        assert_eq!(out.committed(), 9);
        let w = out.into_inner();
        assert_eq!(joined(&w.chunks), "helloworl");
        assert_eq!(Metrics::get(&metrics.output_bytes), 9);
    }

    #[test]
    fn into_inner_after_a_failure_hands_back_exactly_the_committed_bytes() {
        let limits = Limits {
            max_output_bytes: Some(5),
            ..Limits::default()
        };
        let mut out = WriteOut::new(Chunks::default())
            .with_budget(100)
            .with_limits(&limits);
        out.write_str("abc").unwrap();
        let err = out.write_str("xyz").unwrap_err();
        assert_eq!(err.code, Code::ResourceLimitExceeded);
        assert!(!err.committed_output);
        assert_eq!(out.committed(), 0);
        let w = out.into_inner();
        assert!(w.chunks.is_empty(), "no committed output means none: {w:?}");
        assert_eq!(w.flushes, 0);
    }

    #[test]
    fn a_caller_that_wants_the_partial_output_flushes_before_into_inner() {
        let mut out = WriteOut::new(Chunks::default()).with_budget(100);
        out.write_str("abc").unwrap();
        out.flush().unwrap();
        let w = out.into_inner();
        assert_eq!(joined(&w.chunks), "abc");
        assert_eq!(w.flushes, 1);
    }

    #[test]
    fn a_limit_failure_with_nothing_written_is_not_committed() {
        let limits = Limits {
            max_output_bytes: Some(3),
            ..Limits::default()
        };
        let mut out = WriteOut::new(Chunks::default()).with_limits(&limits);
        let err = out.write_str("abcd").unwrap_err();
        assert_eq!(err.code, Code::ResourceLimitExceeded);
        assert!(!err.committed_output);
        assert_eq!(out.into_inner().chunks, Vec::<Vec<u8>>::new());
    }

    #[test]
    fn an_io_error_is_output_failed_and_says_whether_bytes_were_committed() {
        let writer = Chunks {
            fail_after: Some(4),
            ..Chunks::default()
        };
        let mut out = WriteOut::new(writer).with_budget(3);
        out.write_str("abc").unwrap();
        assert_eq!(out.committed(), 3);
        out.write_str("de").unwrap();
        assert_eq!(out.committed(), 3);
        let err = out.flush().unwrap_err();
        assert_eq!(err.code, Code::OutputFailed);
        assert!(err.committed_output);
        assert!(err.message.contains("disk full"));

        let writer = Chunks {
            fail_after: Some(0),
            ..Chunks::default()
        };
        let mut out = WriteOut::new(writer).with_budget(0);
        let err = out.write_str("x").unwrap_err();
        assert_eq!(err.code, Code::OutputFailed);
        assert!(!err.committed_output);
    }

    #[test]
    fn metrics_count_bytes_handed_to_the_writer() {
        let metrics = Metrics::new();
        let mut out = WriteOut::new(Vec::new())
            .with_budget(100)
            .with_metrics(Arc::clone(&metrics));
        out.write_str("twelve bytes").unwrap();
        assert_eq!(Metrics::get(&metrics.output_bytes), 0);
        out.flush().unwrap();
        assert_eq!(Metrics::get(&metrics.output_bytes), 12);
        assert_eq!(out.into_inner(), b"twelve bytes");
    }

    #[test]
    fn string_out_keeps_the_text() {
        let mut s = StringOut::new();
        s.write_str("a").unwrap();
        s.write_str("b").unwrap();
        s.flush().unwrap();
        assert_eq!(s.as_str(), "ab");
        assert_eq!(s.into_string(), "ab");
    }

    #[test]
    fn join_separates_items_not_fragments() {
        let mut j = Join::new(StringOut::new(), ", ");
        j.item_start().unwrap();
        j.write_str("a").unwrap();
        j.write_str("b").unwrap();
        j.item_end().unwrap();
        j.item_start().unwrap();
        j.write_str("c").unwrap();
        j.item_end().unwrap();
        assert_eq!(j.items(), 2);
        assert_eq!(j.into_inner().as_str(), "ab, c");
    }

    #[test]
    fn join_counts_empty_items() {
        let mut j = Join::new(StringOut::new(), ",");
        for _ in 0..3 {
            j.item_start().unwrap();
            j.item_end().unwrap();
        }
        j.item_start().unwrap();
        j.write_str("x").unwrap();
        j.item_end().unwrap();
        j.item_start().unwrap();
        j.item_end().unwrap();
        assert_eq!(j.into_inner().as_str(), ",,,x,");
    }

    #[test]
    fn join_treats_a_fragment_outside_an_item_as_an_item() {
        let mut j = Join::new(StringOut::new(), "|");
        j.write_str("a").unwrap();
        j.write_str("").unwrap();
        j.write_str("b").unwrap();
        j.flush().unwrap();
        assert_eq!(j.into_inner().as_str(), "a||b");
    }

    #[test]
    fn join_with_no_items_writes_nothing() {
        let mut j = Join::new(StringOut::new(), ",");
        j.flush().unwrap();
        assert_eq!(j.into_inner().as_str(), "");
    }

    #[test]
    fn join_rejects_unbalanced_item_markers() {
        let mut j = Join::new(StringOut::new(), ",");
        assert_eq!(j.item_end().unwrap_err().code, Code::ProtocolOrderError);
        j.item_start().unwrap();
        assert_eq!(j.item_start().unwrap_err().code, Code::ProtocolOrderError);
    }

    /// Feed `text` to a replacer split at `at`, then flushed, and give the
    /// result back.
    fn replaced_split(text: &str, at: usize, from: &str, to: &str) -> String {
        let mut r = ReplaceText::new(StringOut::new(), from, to);
        let (a, b) = text.split_at(at);
        r.write_str(a).unwrap();
        r.write_str(b).unwrap();
        r.flush().unwrap();
        r.into_inner().into_string()
    }

    #[test]
    fn replace_matches_str_replace_when_split_at_every_boundary() {
        let cases = [
            ("abcabc", "abc", "X"),
            ("xxabcxxabcxx", "abc", ""),
            ("aaaa", "aa", "b"),
            ("aaaaa", "aa", "b"),
            ("ababab", "aba", "_"),
            ("no match here", "zzz", "Y"),
            ("abab", "abab", "1"),
            ("ab", "abc", "1"),
            ("héllo wörld héllo", "héllo", "hi"),
            ("日本語日本", "日本", "*"),
            ("a\r\nb\r\n", "\r\n", "\n"),
        ];
        for (text, from, to) in cases {
            let want = text.replace(from, to);
            for at in (0..=text.len()).filter(|&i| text.is_char_boundary(i)) {
                assert_eq!(
                    replaced_split(text, at, from, to),
                    want,
                    "{text:?} split at {at} replacing {from:?}"
                );
            }
        }
    }

    #[test]
    fn replace_across_many_one_character_fragments() {
        let text = "the cat sat on the mat with the hat";
        let mut r = ReplaceText::new(StringOut::new(), "the", "a");
        for c in text.chars() {
            let mut buf = [0u8; 4];
            r.write_str(c.encode_utf8(&mut buf)).unwrap();
        }
        r.flush().unwrap();
        assert_eq!(r.into_inner().as_str(), text.replace("the", "a"));
    }

    #[test]
    fn replace_never_carries_more_than_the_literal_less_one_byte() {
        let mut r = ReplaceText::new(StringOut::new(), "abcd", "");
        r.write_str("xxabc").unwrap();
        assert_eq!(r.carry, "abc");
        assert_eq!(r.out.as_str(), "xx");
        r.write_str("ab").unwrap();
        assert_eq!(r.carry, "ab");
        assert_eq!(r.out.as_str(), "xxabc");
        r.write_str("cdab").unwrap();
        assert_eq!(r.carry, "ab");
        assert_eq!(r.out.as_str(), "xxabc");
        r.flush().unwrap();
        assert_eq!(r.carry, "");
        assert_eq!(r.into_inner().as_str(), "xxabcab");
    }

    #[test]
    fn replace_with_an_empty_literal_passes_text_through() {
        let mut r = ReplaceText::new(StringOut::new(), "", "X");
        r.write_str("abc").unwrap();
        r.flush().unwrap();
        assert_eq!(r.into_inner().as_str(), "abc");
    }

    #[test]
    fn replace_flushes_the_carry_at_flush_so_a_later_match_cannot_span_it() {
        let mut r = ReplaceText::new(StringOut::new(), "ab", "X");
        r.write_str("a").unwrap();
        r.flush().unwrap();
        r.write_str("b").unwrap();
        r.flush().unwrap();
        assert_eq!(r.into_inner().as_str(), "ab");
    }

    #[test]
    fn combinators_stack_over_a_writer() {
        let inner = WriteOut::new(Vec::new()).with_budget(3);
        let mut j = Join::new(ReplaceText::new(inner, "-", "+"), ";");
        j.write_str("a-b").unwrap();
        j.write_str("c-").unwrap();
        j.flush().unwrap();
        let bytes = j.into_inner().into_inner().into_inner();
        assert_eq!(String::from_utf8(bytes).unwrap(), "a+b;c+");
    }
}
