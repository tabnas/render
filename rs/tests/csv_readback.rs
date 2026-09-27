//! The rendered CSV, read back by an independent reader.
//!
//! Exact bytes are asserted beside the renderer; this test asks the other
//! question, whether a reader that knows nothing of this crate gets the
//! cells back. The `csv` crate is that reader. A disagreement here is a
//! defect in the renderer whatever the bytes look like.

use tabnas_render::{CsvOptions, CsvRenderer, MissingText, Newline, Quoting, StringOut};
use tabnas_transduce::{Cell, PublicColumn, TableEvent, TableSink};

/// A table whose cells exercise every quoting hazard at once.
fn hazards() -> (Vec<PublicColumn>, Vec<Vec<Cell>>, Vec<Vec<&'static str>>) {
    let columns = [
        "plain", "empty", "comma", "quote", "breaks", "unicode", "number", "flag",
    ]
    .iter()
    .map(|l| PublicColumn::new(*l))
    .collect();
    let rows = vec![
        vec![
            Cell::String("ada".into()),
            Cell::String("".into()),
            Cell::String("x, y".into()),
            Cell::String("say \"hi\"".into()),
            Cell::String("a\r\nb\nc\rd".into()),
            Cell::String("héllo 日本語 🚀".into()),
            Cell::Number {
                value: 50.25,
                lexeme: Some("50.250".into()),
            },
            Cell::Bool(true),
        ],
        vec![
            Cell::String("\"".into()),
            Cell::Null,
            Cell::String(",".into()),
            Cell::String("\"\"".into()),
            Cell::String("\n".into()),
            Cell::String("→".into()),
            Cell::Number {
                value: 0.0,
                lexeme: None,
            },
            Cell::Bool(false),
        ],
        vec![
            Cell::Missing,
            Cell::String(" ".into()),
            Cell::String(",,".into()),
            Cell::String("a\"b".into()),
            Cell::String("\r".into()),
            Cell::String("ß".into()),
            Cell::Number {
                value: -1.5e300,
                lexeme: Some("-1.5E+300".into()),
            },
            Cell::Bool(true),
        ],
    ];
    let expected = vec![
        vec![
            "ada",
            "",
            "x, y",
            "say \"hi\"",
            "a\r\nb\nc\rd",
            "héllo 日本語 🚀",
            "50.250",
            "true",
        ],
        vec!["\"", "", ",", "\"\"", "\n", "→", "0", "false"],
        vec!["?", " ", ",,", "a\"b", "\r", "ß", "-1.5E+300", "true"],
    ];
    (columns, rows, expected)
}

fn render(options: CsvOptions) -> (String, Vec<Vec<&'static str>>) {
    let (columns, rows, expected) = hazards();
    let mut r = CsvRenderer::new(StringOut::new(), options).unwrap();
    r.table_event(TableEvent::Schema(&columns)).unwrap();
    for row in &rows {
        r.table_event(TableEvent::Row(row)).unwrap();
    }
    r.table_event(TableEvent::End).unwrap();
    (r.into_inner().into_string(), expected)
}

fn read_back(text: &str, delimiter: u8) -> (Vec<String>, Vec<Vec<String>>) {
    let mut reader = csv::ReaderBuilder::new()
        .delimiter(delimiter)
        .has_headers(true)
        .flexible(false)
        .from_reader(text.as_bytes());
    let headers: Vec<String> = reader
        .headers()
        .unwrap()
        .iter()
        .map(str::to_owned)
        .collect();
    let records: Vec<Vec<String>> = reader
        .records()
        .map(|r| r.unwrap().iter().map(str::to_owned).collect())
        .collect();
    (headers, records)
}

fn options() -> CsvOptions {
    CsvOptions {
        missing: MissingText::Text("?".into()),
        ..CsvOptions::default()
    }
}

fn assert_read_back(text: &str, delimiter: u8, expected: &[Vec<&str>]) {
    let (headers, records) = read_back(text, delimiter);
    assert_eq!(
        headers,
        ["plain", "empty", "comma", "quote", "breaks", "unicode", "number", "flag"]
    );
    assert_eq!(records.len(), expected.len());
    for (record, want) in records.iter().zip(expected) {
        assert_eq!(record, want);
    }
}

#[test]
fn the_standard_profile_reads_back_cell_for_cell() {
    let (text, expected) = render(options());
    assert_read_back(&text, b',', &expected);
}

#[test]
fn minimal_quoting_reads_back_cell_for_cell() {
    let (text, expected) = render(CsvOptions {
        quoting: Quoting::Minimal,
        ..options()
    });
    assert_read_back(&text, b',', &expected);
}

#[test]
fn a_tab_delimited_lf_dialect_reads_back_cell_for_cell() {
    let (text, expected) = render(CsvOptions {
        delimiter: '\t',
        newline: Newline::Lf,
        ..options()
    });
    assert_read_back(&text, b'\t', &expected);
    let (text, expected) = render(CsvOptions {
        delimiter: ';',
        newline: Newline::Lf,
        quoting: Quoting::Minimal,
        ..options()
    });
    assert_read_back(&text, b';', &expected);
}

#[test]
fn many_rows_read_back_with_the_right_count() {
    let columns = [PublicColumn::new("i"), PublicColumn::new("text")];
    let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
    r.table_event(TableEvent::Schema(&columns)).unwrap();
    for i in 0..1000 {
        let row = [
            Cell::Number {
                value: i as f64,
                lexeme: None,
            },
            Cell::String(format!("row {i}, \"quoted\"\n").into()),
        ];
        r.table_event(TableEvent::Row(&row)).unwrap();
    }
    r.table_event(TableEvent::End).unwrap();
    let text = r.into_inner().into_string();
    let (_, records) = read_back(&text, b',');
    assert_eq!(records.len(), 1000);
    assert_eq!(records[999][0], "999");
    assert_eq!(records[999][1], "row 999, \"quoted\"\n");
}
