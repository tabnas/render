// test/spec/csv.tsv: `TableRows/1` events and `CsvOptions` in, the exact
// CSV text out, or the code of the first failure. The encodings are
// docs/reference.md's "Shared fixtures".

mod common;

use serde_json::Value as Json;
use tabnas_render::{CsvOptions, CsvRenderer, MissingText, Newline, Quoting, StringOut};
use tabnas_support::{Row, Runner, Value};

use common::{expected_text, failure, feed_table, field, json_column, known_fields, malformed};

fn options(row: &Row) -> CsvOptions {
    let json = json_column(row, "options", "{}");
    known_fields(
        row,
        &json,
        &[
            "delimiter",
            "newline",
            "header",
            "null_text",
            "missing",
            "quoting",
        ],
    );
    let mut options = CsvOptions::default();
    if let Some(d) = field(row, &json, "delimiter") {
        let mut chars = d.as_str().unwrap_or("").chars();
        match (chars.next(), chars.next()) {
            (Some(c), None) => options.delimiter = c,
            _ => malformed(row, format!("delimiter {d} is not one character")),
        }
    }
    match field(row, &json, "newline").map(|n| n.as_str()) {
        None => {}
        Some(Some("lf")) => options.newline = Newline::Lf,
        Some(Some("crlf")) => options.newline = Newline::CrLf,
        Some(other) => malformed(row, format!("newline {other:?}")),
    }
    match field(row, &json, "header") {
        None => {}
        Some(Json::Bool(b)) => options.header = *b,
        Some(other) => malformed(row, format!("header {other}")),
    }
    match field(row, &json, "null_text") {
        None => {}
        Some(Json::String(s)) => options.null_text = s.as_str().into(),
        Some(other) => malformed(row, format!("null_text {other}")),
    }
    match field(row, &json, "missing") {
        None | Some(Json::Null) => {}
        Some(Json::String(s)) => options.missing = MissingText::Text(s.as_str().into()),
        Some(other) => malformed(row, format!("missing {other}")),
    }
    match field(row, &json, "quoting").map(|n| n.as_str()) {
        None => {}
        Some(Some("always")) => options.quoting = Quoting::Always,
        Some(Some("minimal")) => options.quoting = Quoting::Minimal,
        Some(other) => malformed(row, format!("quoting {other:?}")),
    }
    options
}

#[test]
fn csv() {
    Runner::new_with_row(|_input, row| {
        let events = common::table_events(row, &json_column(row, "events", ""));
        let mut renderer =
            CsvRenderer::new(StringOut::new(), options(row)).map_err(|f| failure(&f))?;
        feed_table(&mut renderer, &events).map_err(|f| failure(&f))?;
        Ok(Value::String(renderer.into_inner().into_string()))
    })
    .input("events")
    .expected("expected")
    .parse_expected(expected_text)
    .file(common::spec("csv.tsv"));
}
