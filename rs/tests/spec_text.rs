// test/spec/text.tsv: the text algebra as data. A row names a stack of
// text stages over a coalescing writer and a script of operations on the
// outermost stage; the expected value is the JSON array of the chunks the
// writer received, one per `write` call, or the code of the first failing
// operation. The encodings are docs/reference.md's "Shared fixtures".

mod common;

use std::cell::RefCell;
use std::io;
use std::rc::Rc;

use serde_json::Value as Json;
use tabnas_render::{Concat, Join, ReplaceText, TextOut, WriteOut, DEFAULT_BUDGET};
use tabnas_support::{Row, Runner, Value};
use tabnas_transduce::{Fail, Limits};

use common::{failure, field, json_column, known_fields, malformed};

/// A writer that keeps each `write` call as one chunk and takes it whole.
#[derive(Clone, Default)]
struct Recorder(Rc<RefCell<Vec<String>>>);

impl io::Write for Recorder {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let text = String::from_utf8(buf.to_vec())
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        self.0.borrow_mut().push(text);
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

/// The outermost stage, kept by type so the item markers can reach it.
enum Top {
    Writer(Box<dyn TextOut>),
    Join(Join<Box<dyn TextOut>>),
    Concat(Concat<Box<dyn TextOut>>),
    Replace(ReplaceText<Box<dyn TextOut>>),
}

impl Top {
    fn out(&mut self) -> &mut dyn TextOut {
        match self {
            Top::Writer(o) => o.as_mut(),
            Top::Join(o) => o,
            Top::Concat(o) => o,
            Top::Replace(o) => o,
        }
    }

    fn item(&mut self, row: &Row, start: bool) -> Result<(), Fail> {
        match (self, start) {
            (Top::Join(j), true) => j.item_start(),
            (Top::Join(j), false) => j.item_end(),
            (Top::Concat(c), true) => c.item_start(),
            (Top::Concat(c), false) => c.item_end(),
            _ => malformed(row, "item markers need a join or concat outermost"),
        }
    }

    fn into_box(self) -> Box<dyn TextOut> {
        match self {
            Top::Writer(o) => o,
            Top::Join(o) => Box::new(o),
            Top::Concat(o) => Box::new(o),
            Top::Replace(o) => Box::new(o),
        }
    }
}

fn text(row: &Row, json: &Json) -> String {
    match json {
        Json::String(s) => s.clone(),
        other => malformed(row, format!("{other} is not a string")),
    }
}

/// Build the stack: the writer, then the stages from the innermost (the
/// last listed) outwards.
fn build(row: &Row, recorder: Recorder) -> Top {
    let pipeline = json_column(row, "pipeline", "{}");
    known_fields(row, &pipeline, &["budget", "limit", "stages"]);
    let mut writer = WriteOut::new(recorder);
    writer = writer.with_budget(match field(row, &pipeline, "budget") {
        None => DEFAULT_BUDGET,
        Some(n) => n
            .as_u64()
            .unwrap_or_else(|| malformed(row, format!("budget {n}"))) as usize,
    });
    if let Some(n) = field(row, &pipeline, "limit") {
        let limits = Limits {
            max_output_bytes: Some(
                n.as_u64()
                    .unwrap_or_else(|| malformed(row, format!("limit {n}"))),
            ),
            ..Limits::default()
        };
        writer = writer.with_limits(&limits);
    }
    let mut top = Top::Writer(Box::new(writer));
    let stages = match field(row, &pipeline, "stages") {
        None => Vec::new(),
        Some(Json::Array(stages)) => stages.clone(),
        Some(other) => malformed(row, format!("stages {other}")),
    };
    for stage in stages.iter().rev() {
        let inner = top.into_box();
        top = match stage {
            Json::Object(o) if o.len() == 1 => match o.iter().next() {
                Some((k, sep)) if k == "join" => Top::Join(Join::new(inner, text(row, sep))),
                Some((k, Json::Bool(true))) if k == "concat" => Top::Concat(Concat::new(inner)),
                Some((k, Json::Array(pair))) if k == "replace" && pair.len() == 2 => Top::Replace(
                    ReplaceText::new(inner, text(row, &pair[0]), text(row, &pair[1])),
                ),
                _ => malformed(row, format!("{stage} is not a stage")),
            },
            other => malformed(row, format!("{other} is not a stage")),
        };
    }
    top
}

fn run(row: &Row) -> Result<Value, tabnas_support::Failure> {
    let recorder = Recorder::default();
    let chunks = Rc::clone(&recorder.0);
    let mut top = build(row, recorder);
    let Json::Array(ops) = json_column(row, "ops", "") else {
        malformed(row, "ops is a JSON array")
    };
    for op in &ops {
        let done = match op {
            Json::String(fragment) => top.out().write_str(fragment),
            Json::Object(o) if o.len() == 1 => match o.get("op").and_then(Json::as_str) {
                Some("start") => top.item(row, true),
                Some("end") => top.item(row, false),
                Some("flush") => top.out().flush(),
                _ => malformed(row, format!("{op} is not an operation")),
            },
            other => malformed(row, format!("{other} is not an operation")),
        };
        done.map_err(|f| failure(&f))?;
    }
    // Dropped without a flush, as `into_inner` drops it: what is still
    // buffered never reached the writer, and is not in the result.
    drop(top);
    let chunks = chunks.borrow().clone();
    Ok(Value::Array(
        chunks.into_iter().map(Value::String).collect(),
    ))
}

#[test]
fn text_algebra() {
    Runner::new_with_row(|_input, row| run(row))
        .input("ops")
        .expected("expected")
        .file(common::spec("text.tsv"));
}
