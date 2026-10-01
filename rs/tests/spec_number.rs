// test/spec/number.tsv: one number (a lexeme, a value, or both) in, the
// text both renderers write for it out, or the code that refuses it.
//
// Each row runs twice, through the JSON renderer as a root scalar and
// through the CSV renderer as the one field of a minimally quoted,
// headerless, LF-terminated table, and the two must agree before the
// row is compared: a number is the one thing the two renderers share.

mod common;

use tabnas_render::StringOut;
use tabnas_render::{CsvOptions, CsvRenderer, JsonOptions, JsonRenderer, Newline, Quoting};
use tabnas_support::{Failure, Row, Runner, Value};
use tabnas_transduce::{Cell, Fail, JsonEvent, Number, PublicColumn, Sink, TableEvent, TableSink};

use common::{expected_text, failure, json_column, malformed};

fn through_json(value: f64, lexeme: Option<&str>) -> Result<String, Fail> {
    let mut r = JsonRenderer::new(StringOut::new(), JsonOptions::default());
    r.event(JsonEvent::Number(Number { value, lexeme }))?;
    r.event(JsonEvent::End)?;
    Ok(r.into_inner().into_string())
}

fn through_csv(value: f64, lexeme: Option<&str>) -> Result<String, Fail> {
    let options = CsvOptions {
        header: false,
        newline: Newline::Lf,
        quoting: Quoting::Minimal,
        ..CsvOptions::default()
    };
    let mut r = CsvRenderer::new(StringOut::new(), options)?;
    r.table_event(TableEvent::Schema(&[PublicColumn::new("n")]))?;
    r.table_event(TableEvent::Row(&[Cell::Number {
        value,
        lexeme: lexeme.map(Into::into),
    }]))?;
    r.table_event(TableEvent::End)?;
    let text = r.into_inner().into_string();
    Ok(text.strip_suffix('\n').unwrap_or(&text).to_string())
}

fn render(row: &Row) -> Result<Value, Failure> {
    let json = json_column(row, "number", "");
    let serde_json::Value::Object(object) = &json else {
        malformed(row, "number is a number object")
    };
    let (value, lexeme) = common::number(row, object);
    let a = through_json(value, lexeme.as_deref());
    let b = through_csv(value, lexeme.as_deref());
    match (&a, &b) {
        (Ok(x), Ok(y)) if x == y => Ok(Value::String(x.clone())),
        (Err(x), Err(y)) if x.code == y.code => Err(failure(x)),
        _ => panic!(
            "{}: the renderers disagree: JSON {a:?}, CSV {b:?}",
            row.location()
        ),
    }
}

#[test]
fn number() {
    Runner::new_with_row(|_input, row| render(row))
        .input("number")
        .expected("expected")
        .parse_expected(expected_text)
        .file(common::spec("number.tsv"));
}
