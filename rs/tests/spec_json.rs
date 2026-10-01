// test/spec/json.tsv: `JsonEvents/1` events and `JsonOptions` in, the
// exact JSON text out, or the code of the first failure. The encodings
// are docs/reference.md's "Shared fixtures".

mod common;

use serde_json::Value as Json;
use tabnas_render::{JsonOptions, JsonRenderer, StringOut};
use tabnas_support::{Row, Runner, Value};

use common::{expected_text, failure, feed_json, field, json_column, known_fields, malformed};

fn options(row: &Row) -> JsonOptions {
    let json = json_column(row, "options", "{}");
    known_fields(row, &json, &["indent", "trailing_newline"]);
    let mut options = JsonOptions::default();
    match field(row, &json, "indent") {
        None | Some(Json::Null) => {}
        Some(n) => match n.as_u64() {
            Some(n) => options.indent = Some(n as usize),
            None => malformed(row, format!("indent {n}")),
        },
    }
    match field(row, &json, "trailing_newline") {
        None => {}
        Some(Json::Bool(b)) => options.trailing_newline = *b,
        Some(other) => malformed(row, format!("trailing_newline {other}")),
    }
    options
}

#[test]
fn json() {
    Runner::new_with_row(|_input, row| {
        let events = common::json_events(row, &json_column(row, "events", ""));
        let mut renderer = JsonRenderer::new(StringOut::new(), options(row));
        feed_json(&mut renderer, &events).map_err(|f| failure(&f))?;
        Ok(Value::String(renderer.into_inner().into_string()))
    })
    .input("events")
    .expected("expected")
    .parse_expected(expected_text)
    .file(common::spec("json.tsv"));
}
