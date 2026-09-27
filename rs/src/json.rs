//! `JsonEvents/1` as JSON text.
//!
//! The renderer writes what it is given, in the order it is given, with
//! nothing held back but the separators: a comma is written when the next
//! item begins, never speculatively, so an aborted document is still a
//! prefix of a valid one. Compact output is the standard profile; a fixed
//! indent is a separate profile for people rather than programs, and the
//! two differ only in whitespace. Strings are escaped as RFC 8259 requires
//! and no more: `"`, `\`, and the control characters, with everything else,
//! non-ASCII included, written as itself, because the output is UTF-8 and
//! an escape would only make it longer.
//!
//! The renderer validates the event sequence as it goes, because a
//! third-party source is as much a source of `JsonEvents/1` as the
//! standard ones are: one root value, keys only where a member begins,
//! balanced containers, one end.

use tabnas_transduce::{write_json_string, Fail, Flow, JsonEvent, Number, Sink};

use crate::number::number_text;
use crate::text::TextOut;

/// The JSON profile.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct JsonOptions {
    /// Spaces per nesting level, with a newline before every item and
    /// every closing bracket of a non-empty container. `None` or `Some(0)`
    /// is compact: no whitespace at all.
    pub indent: Option<usize>,
    /// Write a newline after the root value, at `End`.
    pub trailing_newline: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Frame {
    Object { first: bool, expecting_key: bool },
    Array { first: bool },
}

/// Renders `JsonEvents/1` as JSON text.
///
/// Exactly one root value, then `End`; a second root, an `End` before the
/// root or with a container open, a key outside an object or where a value
/// is due, a value where a key is due, an unbalanced or mismatched close,
/// and any event after `End` are `PROTOCOL_ORDER_ERROR`. Numbers write
/// their lexeme when it is a JSON number (`INVALID_NUMBER` otherwise) and
/// the shortest round-trip form of the value when there is none; NaN and
/// infinity have no JSON form and are `TARGET_VALUE_UNREPRESENTABLE`. The
/// output is flushed once, at `End`; a failure found after any text was
/// written says so with `committed_output`.
pub struct JsonRenderer<O: TextOut> {
    out: O,
    options: JsonOptions,
    /// Spaces per level; zero is compact.
    indent: usize,
    stack: Vec<Frame>,
    root_done: bool,
    ended: bool,
    emitted: bool,
    scratch: String,
    /// Spaces, grown to the widest indentation written so far.
    pad: String,
}

impl<O: TextOut> JsonRenderer<O> {
    pub fn new(out: O, options: JsonOptions) -> Self {
        JsonRenderer {
            indent: options.indent.unwrap_or(0),
            out,
            options,
            stack: Vec::new(),
            root_done: false,
            ended: false,
            emitted: false,
            scratch: String::new(),
            pad: String::new(),
        }
    }

    pub fn options(&self) -> &JsonOptions {
        &self.options
    }

    /// Containers currently open.
    pub fn depth(&self) -> usize {
        self.stack.len()
    }

    /// Whether `End` has been rendered.
    pub fn is_done(&self) -> bool {
        self.ended
    }

    pub fn into_inner(self) -> O {
        self.out
    }

    fn fail(&self, f: Fail) -> Fail {
        if self.emitted {
            f.committed()
        } else {
            f
        }
    }

    fn protocol(&self, message: &str) -> Fail {
        self.fail(Fail::protocol(message))
    }

    fn put(&mut self, s: &str) -> Result<(), Fail> {
        self.emitted = true;
        self.out.write_str(s)
    }

    /// The escaped form of `s`, written through the reused scratch buffer.
    fn put_string(&mut self, s: &str) -> Result<(), Fail> {
        let mut scratch = std::mem::take(&mut self.scratch);
        scratch.clear();
        write_json_string(s, &mut scratch);
        let r = self.put(&scratch);
        self.scratch = scratch;
        r
    }

    fn put_number(&mut self, n: Number<'_>) -> Result<(), Fail> {
        let mut scratch = std::mem::take(&mut self.scratch);
        let r = match number_text(n.value, n.lexeme, &mut scratch) {
            Ok(text) => self.put(text),
            Err(f) => Err(self.fail(f)),
        };
        self.scratch = scratch;
        r
    }

    /// A line break and the indentation of `depth` levels; nothing when
    /// compact.
    fn break_line(&mut self, depth: usize) -> Result<(), Fail> {
        if self.indent == 0 {
            return Ok(());
        }
        let width = depth.saturating_mul(self.indent);
        while self.pad.len() < width {
            self.pad.push(' ');
        }
        let pad = std::mem::take(&mut self.pad);
        self.put("\n")?;
        // `pad` is ASCII spaces at least `width` long, so the slice is on a
        // char boundary and within bounds.
        let r = self.put(&pad[..width]);
        self.pad = pad;
        r
    }

    /// The separators before a value, and the check that one may begin.
    fn begin_value(&mut self) -> Result<(), Fail> {
        if self.ended {
            return Err(self.protocol("a value after the end"));
        }
        let depth = self.stack.len();
        match self.stack.last_mut() {
            None if self.root_done => Err(self.protocol("a second root value")),
            None => Ok(()),
            Some(Frame::Object {
                expecting_key: true,
                ..
            }) => Err(self.protocol("a value where a key is due")),
            Some(Frame::Object { .. }) => Ok(()),
            Some(Frame::Array { first }) => {
                let comma = !*first;
                *first = false;
                if comma {
                    self.put(",")?;
                }
                self.break_line(depth)
            }
        }
    }

    /// Bookkeeping after a whole value.
    fn end_value(&mut self) {
        match self.stack.last_mut() {
            None => self.root_done = true,
            Some(Frame::Object { expecting_key, .. }) => *expecting_key = true,
            Some(Frame::Array { .. }) => {}
        }
    }

    fn key(&mut self, k: &str) -> Result<(), Fail> {
        if self.ended {
            return Err(self.protocol("a key after the end"));
        }
        let depth = self.stack.len();
        match self.stack.last_mut() {
            Some(Frame::Object {
                first,
                expecting_key: true,
            }) => {
                let comma = !*first;
                *first = false;
                if comma {
                    self.put(",")?;
                }
                self.break_line(depth)?;
                self.put_string(k)?;
                self.put(if self.indent > 0 { ": " } else { ":" })?;
                // Only after the key is on the wire, so a write failure
                // cannot leave the frame half updated.
                if let Some(Frame::Object { expecting_key, .. }) = self.stack.last_mut() {
                    *expecting_key = false;
                }
                Ok(())
            }
            Some(Frame::Object { .. }) => Err(self.protocol("a key where a value is due")),
            Some(Frame::Array { .. }) => Err(self.protocol("a key inside an array")),
            None => Err(self.protocol("a key outside an object")),
        }
    }

    fn start(&mut self, open: &str, frame: Frame) -> Result<(), Fail> {
        self.begin_value()?;
        self.put(open)?;
        self.stack.push(frame);
        Ok(())
    }

    fn close_object(&mut self) -> Result<(), Fail> {
        if self.ended {
            return Err(self.protocol("an object end after the end"));
        }
        let first = match self.stack.last() {
            Some(Frame::Object {
                expecting_key: false,
                ..
            }) => return Err(self.protocol("an object ended after a key with no value")),
            Some(Frame::Object { first, .. }) => *first,
            Some(Frame::Array { .. }) => return Err(self.protocol("an object end inside an array")),
            None => return Err(self.protocol("an object end with no open object")),
        };
        self.stack.pop();
        if !first {
            self.break_line(self.stack.len())?;
        }
        self.put("}")?;
        self.end_value();
        Ok(())
    }

    fn close_array(&mut self) -> Result<(), Fail> {
        if self.ended {
            return Err(self.protocol("an array end after the end"));
        }
        let first = match self.stack.last() {
            Some(Frame::Array { first }) => *first,
            Some(Frame::Object { .. }) => {
                return Err(self.protocol("an array end inside an object"))
            }
            None => return Err(self.protocol("an array end with no open array")),
        };
        self.stack.pop();
        if !first {
            self.break_line(self.stack.len())?;
        }
        self.put("]")?;
        self.end_value();
        Ok(())
    }

    fn scalar(&mut self, ev: JsonEvent<'_>) -> Result<(), Fail> {
        self.begin_value()?;
        match ev {
            JsonEvent::Null => self.put("null")?,
            JsonEvent::Bool(true) => self.put("true")?,
            JsonEvent::Bool(false) => self.put("false")?,
            JsonEvent::Number(n) => self.put_number(n)?,
            JsonEvent::String(s) => self.put_string(s)?,
            // `scalar` is called for the four scalar events only.
            _ => return Err(self.protocol("not a scalar")),
        }
        self.end_value();
        Ok(())
    }

    fn end(&mut self) -> Result<(), Fail> {
        if self.ended {
            return Err(self.protocol("a second end"));
        }
        if !self.stack.is_empty() {
            return Err(self.protocol(&format!(
                "the end with {} open container(s)",
                self.stack.len()
            )));
        }
        if !self.root_done {
            return Err(self.protocol("the end before a root value"));
        }
        if self.options.trailing_newline {
            self.put("\n")?;
        }
        self.ended = true;
        self.out.flush()
    }
}

impl<O: TextOut> Sink for JsonRenderer<O> {
    fn event(&mut self, ev: JsonEvent<'_>) -> Result<Flow, Fail> {
        match ev {
            JsonEvent::ObjectStart => self.start(
                "{",
                Frame::Object {
                    first: true,
                    expecting_key: true,
                },
            )?,
            JsonEvent::ArrayStart => self.start("[", Frame::Array { first: true })?,
            JsonEvent::ObjectEnd => self.close_object()?,
            JsonEvent::ArrayEnd => self.close_array()?,
            JsonEvent::Key(k) => self.key(k)?,
            JsonEvent::Null | JsonEvent::Bool(_) | JsonEvent::Number(_) | JsonEvent::String(_) => {
                self.scalar(ev)?
            }
            JsonEvent::End => self.end()?,
        }
        Ok(Flow::Continue)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::text::{StringOut, WriteOut};
    use tabnas_transduce::Code;
    use JsonEvent::*;

    fn num(lexeme: &str) -> JsonEvent<'_> {
        Number(tabnas_transduce::Number::with_lexeme(
            lexeme.parse().unwrap_or(0.0),
            lexeme,
        ))
    }

    fn value(v: f64) -> JsonEvent<'static> {
        Number(tabnas_transduce::Number::new(v))
    }

    fn render(options: JsonOptions, events: &[JsonEvent<'_>]) -> Result<std::string::String, Fail> {
        let mut r = JsonRenderer::new(StringOut::new(), options);
        for ev in events {
            r.event(*ev)?;
        }
        Ok(r.into_inner().into_string())
    }

    fn compact(events: &[JsonEvent<'_>]) -> Result<std::string::String, Fail> {
        render(JsonOptions::default(), events)
    }

    fn indented(n: usize, events: &[JsonEvent<'_>]) -> std::string::String {
        render(
            JsonOptions {
                indent: Some(n),
                trailing_newline: false,
            },
            events,
        )
        .unwrap()
    }

    const DOC: &[JsonEvent<'static>] = &[
        ObjectStart,
        Key("a"),
        ArrayStart,
        Number(tabnas_transduce::Number {
            value: 1.0,
            lexeme: Some("1"),
        }),
        Number(tabnas_transduce::Number {
            value: 2.5,
            lexeme: None,
        }),
        String("x"),
        Bool(true),
        Null,
        ArrayEnd,
        Key("b"),
        ObjectStart,
        ObjectEnd,
        Key("c"),
        ArrayStart,
        ArrayEnd,
        Key("d"),
        ObjectStart,
        Key("e"),
        Bool(false),
        ObjectEnd,
        ObjectEnd,
        End,
    ];

    #[test]
    fn compact_output_has_no_whitespace() {
        assert_eq!(
            compact(DOC).unwrap(),
            r#"{"a":[1,2.5,"x",true,null],"b":{},"c":[],"d":{"e":false}}"#
        );
    }

    #[test]
    fn an_indent_writes_fixed_nesting_and_keeps_empty_containers_on_one_line() {
        assert_eq!(
            indented(2, DOC),
            "{\n  \"a\": [\n    1,\n    2.5,\n    \"x\",\n    true,\n    null\n  ],\n  \"b\": {},\n  \"c\": [],\n  \"d\": {\n    \"e\": false\n  }\n}"
        );
        assert_eq!(
            indented(4, &[ArrayStart, ArrayStart, Null, ArrayEnd, ArrayEnd, End]),
            "[\n    [\n        null\n    ]\n]"
        );
    }

    #[test]
    fn an_indent_of_zero_is_compact() {
        assert_eq!(indented(0, DOC), compact(DOC).unwrap());
    }

    #[test]
    fn the_trailing_newline_is_written_at_end_when_asked() {
        let options = JsonOptions {
            indent: None,
            trailing_newline: true,
        };
        assert_eq!(render(options, &[Null, End]).unwrap(), "null\n");
        assert_eq!(compact(&[Null, End]).unwrap(), "null");
    }

    #[test]
    fn a_root_scalar_is_a_document() {
        assert_eq!(compact(&[String("x"), End]).unwrap(), "\"x\"");
        assert_eq!(compact(&[num("-0.5e3"), End]).unwrap(), "-0.5e3");
        assert_eq!(compact(&[Bool(false), End]).unwrap(), "false");
    }

    #[test]
    fn strings_are_escaped_as_rfc_8259_requires_and_no_more() {
        let text =
            "q\" b\\ n\n r\r t\t bs\u{8} ff\u{c} nul\0 c1\u{1} us\u{1f} del\u{7f} é 日本 🚀 /";
        assert_eq!(
            compact(&[String(text), End]).unwrap(),
            "\"q\\\" b\\\\ n\\n r\\r t\\t bs\\b ff\\f nul\\u0000 c1\\u0001 us\\u001f del\u{7f} é 日本 🚀 /\""
        );
        assert_eq!(
            compact(&[ObjectStart, Key("k\"\n"), Null, ObjectEnd, End]).unwrap(),
            "{\"k\\\"\\n\":null}"
        );
    }

    #[test]
    fn numbers_keep_their_lexeme_or_take_the_shortest_form() {
        let events = [
            ArrayStart,
            num("1.00"),
            num("123456789012345678901234567890"),
            num("-0"),
            num("1E+2"),
            value(0.0),
            value(1e21),
            value(0.1),
            value(-2.0),
            ArrayEnd,
            End,
        ];
        assert_eq!(
            compact(&events).unwrap(),
            "[1.00,123456789012345678901234567890,-0,1E+2,0,1000000000000000000000,0.1,-2]"
        );
    }

    #[test]
    fn a_lexeme_that_is_not_a_json_number_is_invalid_number() {
        for bad in ["1.", "01", "NaN", "+1", "0x1"] {
            let err = compact(&[ArrayStart, num(bad), ArrayEnd, End]).unwrap_err();
            assert_eq!(err.code, Code::InvalidNumber, "{bad:?}");
            assert!(err.committed_output);
        }
        let err = compact(&[num("1."), End]).unwrap_err();
        assert!(!err.committed_output);
    }

    #[test]
    fn nan_and_infinity_are_unrepresentable() {
        for v in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
            let err = compact(&[value(v), End]).unwrap_err();
            assert_eq!(err.code, Code::TargetValueUnrepresentable);
        }
    }

    fn protocol_error(events: &[JsonEvent<'_>]) -> Fail {
        let err = compact(events).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError, "{events:?}");
        err
    }

    #[test]
    fn a_second_root_is_a_protocol_error() {
        protocol_error(&[Null, Null]);
        protocol_error(&[ObjectStart, ObjectEnd, ArrayStart]);
        protocol_error(&[String("a"), String("b"), End]);
    }

    #[test]
    fn an_end_without_a_complete_root_is_a_protocol_error() {
        assert!(!protocol_error(&[End]).committed_output);
        protocol_error(&[ArrayStart, End]);
        protocol_error(&[ObjectStart, Key("a"), End]);
        protocol_error(&[ObjectStart, Key("a"), Null, End]);
    }

    #[test]
    fn a_key_outside_an_object_or_where_a_value_is_due_is_a_protocol_error() {
        protocol_error(&[Key("a")]);
        protocol_error(&[ArrayStart, Key("a")]);
        protocol_error(&[ObjectStart, Key("a"), Key("b")]);
        protocol_error(&[Null, Key("a")]);
    }

    #[test]
    fn a_value_where_a_key_is_due_is_a_protocol_error() {
        protocol_error(&[ObjectStart, Null]);
        protocol_error(&[ObjectStart, ArrayStart]);
        protocol_error(&[ObjectStart, Key("a"), Null, String("b")]);
    }

    #[test]
    fn an_unbalanced_or_mismatched_close_is_a_protocol_error() {
        protocol_error(&[ObjectEnd]);
        protocol_error(&[ArrayEnd]);
        protocol_error(&[ArrayStart, ObjectEnd]);
        protocol_error(&[ObjectStart, ArrayEnd]);
        protocol_error(&[ObjectStart, Key("a"), ObjectEnd]);
        protocol_error(&[ArrayStart, ArrayEnd, ArrayEnd]);
    }

    #[test]
    fn anything_after_the_end_is_a_protocol_error() {
        for after in [End, Null, Key("a"), ObjectStart, ObjectEnd, ArrayEnd] {
            let err = protocol_error(&[Null, End, after]);
            assert!(err.committed_output);
        }
    }

    #[test]
    fn a_failure_leaves_the_output_a_prefix_of_the_document() {
        let mut r = JsonRenderer::new(StringOut::new(), JsonOptions::default());
        for ev in [ObjectStart, Key("a"), ArrayStart, Null] {
            r.event(ev).unwrap();
        }
        assert_eq!(r.depth(), 2);
        assert_eq!(
            r.event(Key("b")).unwrap_err().code,
            Code::ProtocolOrderError
        );
        assert_eq!(r.into_inner().as_str(), "{\"a\":[null");
    }

    #[test]
    fn end_flushes_the_output_and_nothing_else_does() {
        let mut r = JsonRenderer::new(WriteOut::new(Vec::new()), JsonOptions::default());
        for ev in [ArrayStart, Bool(true), ArrayEnd] {
            assert_eq!(r.event(ev).unwrap(), Flow::Continue);
        }
        assert_eq!(r.out.committed(), 0);
        assert!(!r.is_done());
        r.event(End).unwrap();
        assert!(r.is_done());
        assert_eq!(r.out.committed(), 6);
        assert_eq!(r.into_inner().into_inner(), b"[true]");
    }
}
