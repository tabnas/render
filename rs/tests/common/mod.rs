// Shared helpers for the runners of the shared fixtures in ../../test/spec.
// Cargo compiles this module into EVERY integration test binary, so an
// item only one binary uses is dead code in the others; the allow keeps
// that from being a warning rather than hiding anything real.
//
// The input encodings decoded here are the ones docs/reference.md
// ("Shared fixtures") defines for every runtime. A fixture cell that does
// not follow them is a defect in the fixture, not a rendering failure, so
// it panics with the row's location rather than becoming a code a row
// could accidentally match.
#![allow(dead_code)]

use std::path::{Path, PathBuf};

use serde_json::Value as Json;
use tabnas_render::is_json_number;
use tabnas_support::{find_spec_dir, unescape, Failure, Row, Value};
use tabnas_transduce::{Cell, Fail, OwnedJsonEvent, PublicColumn, Sink, TableEvent, TableSink};

/// The shared `test/spec` directory, found by walking up from the crate
/// rather than by counting `..` hops.
pub fn spec_dir() -> PathBuf {
    find_spec_dir(Some(Path::new(env!("CARGO_MANIFEST_DIR"))))
        .expect("a test/spec directory above rs/")
}

/// The fixture file of that name.
pub fn spec(file: &str) -> PathBuf {
    spec_dir().join(file)
}

/// A failure as the shared runner sees it: the code is the contract, the
/// whole failure goes in the report.
pub fn failure(fail: &Fail) -> Failure {
    Failure::new(fail.code.as_str()).with_message(fail.to_string())
}

/// A malformed fixture cell: loud, with the row it came from.
pub fn malformed(row: &Row, what: impl std::fmt::Display) -> ! {
    panic!("{}: malformed fixture cell: {what}", row.location())
}

/// A JSON column, read RAW: the cell is not passed through the escape
/// codec, because JSON has escapes of its own (`\n` inside a JSON string
/// is two characters, and the codec would turn it into a line feed that
/// JSON does not allow). An empty cell is `default`.
pub fn json_column(row: &Row, name: &str, default: &str) -> Json {
    let cell = row.named(name);
    let text = if cell.is_empty() { default } else { cell };
    serde_json::from_str(text).unwrap_or_else(|e| malformed(row, format!("{name}: {e}")))
}

/// The expected column of a text-producing fixture: the exact output,
/// written through the escape codec (`\r`, `\n`, `\t` and `\\` decoded,
/// everything else as it stands). Reached only for a value row; the
/// runner reads `ERROR:<CODE>` itself.
pub fn expected_text(cell: &str, _row: &Row) -> tabnas_support::Result<Value> {
    Ok(Value::String(unescape(cell)))
}

/// A value spelled as fixture text: a JSON number that is finite as an
/// f64, or `NaN`, `Infinity` or `-Infinity`. Every runtime's decimal
/// parser rounds to the nearest f64, so the spelling names one value
/// everywhere; an overflowing spelling is refused, so infinity is always
/// written as such.
pub fn parse_value(row: &Row, text: &str) -> f64 {
    match text {
        "NaN" => f64::NAN,
        "Infinity" => f64::INFINITY,
        "-Infinity" => f64::NEG_INFINITY,
        _ => Some(text)
            .filter(|t| is_json_number(t))
            .and_then(|t| t.parse::<f64>().ok())
            .filter(|v| v.is_finite())
            .unwrap_or_else(|| malformed(row, format!("{text:?} is not a value"))),
    }
}

/// A number object: `{"num": "<lexeme>"}`, `{"value": "<value>"}`, or
/// both. With a lexeme and no value, the value is the lexeme read as a
/// decimal when it is a JSON number (overflowing to an infinity, as
/// `1e999` does in every runtime) and 0 when it is not; the renderers
/// judge the lexeme before the value, so that 0 never decides a row.
pub fn number(row: &Row, object: &serde_json::Map<String, Json>) -> (f64, Option<String>) {
    for k in object.keys() {
        if k != "num" && k != "value" {
            malformed(row, format!("unknown number field {k:?}"));
        }
    }
    let text = |key: &str| match object.get(key) {
        None => None,
        Some(Json::String(s)) => Some(s.clone()),
        Some(other) => malformed(row, format!("{key} must be a string, not {other}")),
    };
    let lexeme = text("num");
    let value = match (text("value"), &lexeme) {
        (Some(v), _) => parse_value(row, &v),
        (None, Some(l)) if is_json_number(l) => l.parse::<f64>().unwrap_or(0.0),
        (None, Some(_)) => 0.0,
        (None, None) => malformed(row, "a number object names num, value or both"),
    };
    (value, lexeme)
}

/// One `TableRows/1` cell: `null`, `true`, `false`, a JSON string, a
/// number object, or `{"missing": true}`. A bare JSON number is refused:
/// it cannot say whether it carries a lexeme.
pub fn cell(row: &Row, json: &Json) -> Cell {
    match json {
        Json::Null => Cell::Null,
        Json::Bool(b) => Cell::Bool(*b),
        Json::String(s) => Cell::String(s.as_str().into()),
        Json::Object(o) if o.get("missing") == Some(&Json::Bool(true)) && o.len() == 1 => {
            Cell::Missing
        }
        Json::Object(o) => {
            let (value, lexeme) = number(row, o);
            Cell::Number {
                value,
                lexeme: lexeme.map(Into::into),
            }
        }
        other => malformed(row, format!("{other} is not a cell")),
    }
}

/// One owned `TableRows/1` event.
pub enum TableEv {
    Schema(Vec<PublicColumn>),
    Row(Vec<Cell>),
    End,
}

/// The `events` column of a table fixture: `{"schema": [labels]}`,
/// `{"row": [cells]}` and `"end"`, in order.
pub fn table_events(row: &Row, json: &Json) -> Vec<TableEv> {
    let Json::Array(items) = json else {
        malformed(row, "events is a JSON array")
    };
    items
        .iter()
        .map(|item| match item {
            Json::String(s) if s == "end" => TableEv::End,
            Json::Object(o) if o.len() == 1 => match o.iter().next() {
                Some((k, Json::Array(labels))) if k == "schema" => TableEv::Schema(
                    labels
                        .iter()
                        .map(|l| match l {
                            Json::String(s) => PublicColumn::new(s.as_str()),
                            other => malformed(row, format!("label {other} is not a string")),
                        })
                        .collect(),
                ),
                Some((k, Json::Array(cells))) if k == "row" => {
                    TableEv::Row(cells.iter().map(|c| cell(row, c)).collect())
                }
                _ => malformed(row, format!("{item} is not a table event")),
            },
            other => malformed(row, format!("{other} is not a table event")),
        })
        .collect()
}

/// Send table events to a sink in order; the first failure is the result.
pub fn feed_table(sink: &mut impl TableSink, events: &[TableEv]) -> Result<(), Fail> {
    for ev in events {
        let ev = match ev {
            TableEv::Schema(columns) => TableEvent::Schema(columns),
            TableEv::Row(cells) => TableEvent::Row(cells),
            TableEv::End => TableEvent::End,
        };
        sink.table_event(ev)?;
    }
    Ok(())
}

/// The `events` column of a JSON fixture: `"{"`, `"}"`, `"["`, `"]"` and
/// `"end"` for the structural events; `{"key": k}` and `{"str": s}`;
/// `null`, `true`, `false`; a number object.
pub fn json_events(row: &Row, json: &Json) -> Vec<OwnedJsonEvent> {
    let Json::Array(items) = json else {
        malformed(row, "events is a JSON array")
    };
    items
        .iter()
        .map(|item| match item {
            Json::String(s) => match s.as_str() {
                "{" => OwnedJsonEvent::ObjectStart,
                "}" => OwnedJsonEvent::ObjectEnd,
                "[" => OwnedJsonEvent::ArrayStart,
                "]" => OwnedJsonEvent::ArrayEnd,
                "end" => OwnedJsonEvent::End,
                other => malformed(row, format!("{other:?} is not a JSON event")),
            },
            Json::Null => OwnedJsonEvent::Null,
            Json::Bool(b) => OwnedJsonEvent::Bool(*b),
            Json::Object(o) if o.len() == 1 && o.contains_key("key") => match &o["key"] {
                Json::String(k) => OwnedJsonEvent::Key(k.as_str().into()),
                other => malformed(row, format!("key {other} is not a string")),
            },
            Json::Object(o) if o.len() == 1 && o.contains_key("str") => match &o["str"] {
                Json::String(s) => OwnedJsonEvent::String(s.as_str().into()),
                other => malformed(row, format!("str {other} is not a string")),
            },
            Json::Object(o) => {
                let (value, lexeme) = number(row, o);
                OwnedJsonEvent::Number {
                    value,
                    lexeme: lexeme.map(Into::into),
                }
            }
            other => malformed(row, format!("{other} is not a JSON event")),
        })
        .collect()
}

/// Send JSON events to a sink in order; the first failure is the result.
pub fn feed_json(sink: &mut impl Sink, events: &[OwnedJsonEvent]) -> Result<(), Fail> {
    for ev in events {
        sink.event(ev.as_event())?;
    }
    Ok(())
}

/// An options object's field, or `None` when absent.
pub fn field<'a>(row: &Row, options: &'a Json, name: &str) -> Option<&'a Json> {
    match options {
        Json::Object(o) => o.get(name),
        other => malformed(row, format!("options {other} is not an object")),
    }
}

/// Every field of an options object is one of `known`: a misspelt option
/// would otherwise be ignored and the row would test the default.
pub fn known_fields(row: &Row, options: &Json, known: &[&str]) {
    if let Json::Object(o) = options {
        for k in o.keys() {
            if !known.contains(&k.as_str()) {
                malformed(row, format!("unknown option {k:?}"));
            }
        }
    }
}
