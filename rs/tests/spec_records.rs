// test/spec/records.tsv: `TableRows/1` events and a missing policy in,
// through `RecordsToJson` and a compact `JsonRenderer`, the exact JSON
// text out, or the code of the first failure.

mod common;

use serde_json::Value as Json;
use tabnas_render::{JsonOptions, JsonRenderer, MissingRecord, RecordsToJson, StringOut};
use tabnas_support::{Row, Runner, Value};

use common::{expected_text, failure, feed_table, field, json_column, known_fields, malformed};

fn missing(row: &Row) -> MissingRecord {
    let json = json_column(row, "options", "{}");
    known_fields(row, &json, &["missing"]);
    match field(row, &json, "missing").map(Json::as_str) {
        None | Some(Some("skip")) => MissingRecord::Skip,
        Some(Some("null")) => MissingRecord::Null,
        Some(Some("error")) => MissingRecord::Error,
        Some(other) => malformed(row, format!("missing {other:?}")),
    }
}

#[test]
fn records() {
    Runner::new_with_row(|_input, row| {
        let events = common::table_events(row, &json_column(row, "events", ""));
        let renderer = JsonRenderer::new(StringOut::new(), JsonOptions::default());
        let mut records = RecordsToJson::new(renderer).with_missing(missing(row));
        feed_table(&mut records, &events).map_err(|f| failure(&f))?;
        Ok(Value::String(
            records.into_inner().into_inner().into_string(),
        ))
    })
    .input("events")
    .expected("expected")
    .parse_expected(expected_text)
    .file(common::spec("records.tsv"));
}
