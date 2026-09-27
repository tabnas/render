//! `TableRows/1` as CSV: the always-quoted profile.
//!
//! The standard profile quotes every field, doubles `"`, ends every record
//! with CRLF and writes numbers as their lexemes. Always quoting is a
//! decision, not a habit: it makes the output independent of the data (no
//! field can change the record's shape), it makes the empty string and the
//! null text distinguishable from a missing quote pair, and it lets a
//! reader tell that a field was a field. Minimal quoting and other
//! delimiters are dialects the caller selects explicitly, and they are
//! valid here because a row is a finite vector: the renderer sees the whole
//! field before it decides how to write it.
//!
//! The renderer validates the protocol as it goes, because a third-party
//! transducer or a host adapter is as much a source of `TableRows/1` as the
//! standard table transducer is.

use tabnas_transduce::{Cell, Code, Fail, Flow, PublicColumn, TableEvent, TableSink};

use crate::number::{check_number, write_value};
use crate::text::TextOut;

/// The record terminator.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Newline {
    Lf,
    /// RFC 4180's terminator, and the standard profile's.
    #[default]
    CrLf,
}

impl Newline {
    pub fn as_str(self) -> &'static str {
        match self {
            Newline::Lf => "\n",
            Newline::CrLf => "\r\n",
        }
    }
}

/// When a field is quoted.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Quoting {
    /// Every field, the standard profile.
    #[default]
    Always,
    /// Only a field holding the delimiter, `"`, CR or LF. An empty field is
    /// then written as nothing, so the empty string and an empty null text
    /// read back the same; that is the dialect's trade-off, not a defect.
    Minimal,
}

/// What a [`Cell::Missing`] becomes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub enum MissingText {
    /// Fail the run with `MISSING_VALUE`: a table that promised a column
    /// and did not deliver it is not silently padded.
    #[default]
    Error,
    /// Write this text instead.
    Text(Box<str>),
}

/// The CSV dialect.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CsvOptions {
    /// One character, and not `"`, CR, LF or NUL: those would make the
    /// output unreadable by construction, and are refused when the renderer
    /// is built.
    pub delimiter: char,
    pub newline: Newline,
    /// Write the labels as the first record.
    pub header: bool,
    /// The text of a [`Cell::Null`]; empty by default.
    pub null_text: Box<str>,
    pub missing: MissingText,
    pub quoting: Quoting,
}

impl Default for CsvOptions {
    fn default() -> Self {
        CsvOptions {
            delimiter: ',',
            newline: Newline::CrLf,
            header: true,
            null_text: "".into(),
            missing: MissingText::Error,
            quoting: Quoting::Always,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Phase {
    BeforeSchema,
    Rows,
    Done,
}

/// Renders `TableRows/1` as CSV.
///
/// One schema first, rows exactly as wide as the schema, one end: anything
/// else is `PROTOCOL_ORDER_ERROR`. A schema with no columns has no CSV form
/// (a record cannot be empty) and is `TARGET_VALUE_UNREPRESENTABLE`. Every
/// record, the header included, ends with the configured newline, the last
/// one too. The output is flushed once, at `End`, so a table that fails
/// half way is not flushed as if it were whole; a failure found after any
/// text was written says so with `committed_output`.
pub struct CsvRenderer<O: TextOut> {
    out: O,
    options: CsvOptions,
    delimiter: String,
    phase: Phase,
    labels: Vec<Box<str>>,
    rows: u64,
    emitted: bool,
    scratch: String,
}

impl<O: TextOut> CsvRenderer<O> {
    /// A renderer over `out`, or `TARGET_VALUE_UNREPRESENTABLE` when the
    /// delimiter is one no CSV reader could take.
    pub fn new(out: O, options: CsvOptions) -> Result<Self, Fail> {
        if matches!(options.delimiter, '"' | '\r' | '\n' | '\0') {
            return Err(Fail::new(
                Code::TargetValueUnrepresentable,
                format!(
                    "{:?} cannot be a CSV delimiter: it is the quote, a line break or NUL",
                    options.delimiter
                ),
            ));
        }
        Ok(CsvRenderer {
            delimiter: options.delimiter.to_string(),
            out,
            options,
            phase: Phase::BeforeSchema,
            labels: Vec::new(),
            rows: 0,
            emitted: false,
            scratch: String::new(),
        })
    }

    pub fn options(&self) -> &CsvOptions {
        &self.options
    }

    /// Rows written so far.
    pub fn rows(&self) -> u64 {
        self.rows
    }

    /// Whether `End` has been rendered.
    pub fn is_done(&self) -> bool {
        self.phase == Phase::Done
    }

    pub fn into_inner(self) -> O {
        self.out
    }

    /// Mark a failure as leaving partial output when text this renderer
    /// wrote has reached the destination; text still buffered in the
    /// output has not, and the output knows which.
    fn fail(&self, f: Fail) -> Fail {
        if self.emitted && self.out.has_committed() {
            f.committed()
        } else {
            f
        }
    }

    fn schema(&mut self, columns: &[PublicColumn]) -> Result<(), Fail> {
        match self.phase {
            Phase::BeforeSchema => {}
            Phase::Rows => return Err(self.fail(Fail::protocol("a second schema"))),
            Phase::Done => return Err(self.fail(Fail::protocol("a schema after the end"))),
        }
        if columns.is_empty() {
            return Err(Fail::new(
                Code::TargetValueUnrepresentable,
                "a table with no columns has no CSV form",
            ));
        }
        self.labels = columns.iter().map(|c| c.label.clone()).collect();
        self.phase = Phase::Rows;
        if self.options.header {
            self.emitted = true;
            for (i, label) in self.labels.iter().enumerate() {
                if i > 0 {
                    self.out.write_str(&self.delimiter)?;
                }
                write_field(
                    &mut self.out,
                    self.options.quoting,
                    self.options.delimiter,
                    label,
                )?;
            }
            self.out.write_str(self.options.newline.as_str())?;
        }
        Ok(())
    }

    fn row(&mut self, cells: &[Cell]) -> Result<(), Fail> {
        match self.phase {
            Phase::Rows => {}
            Phase::BeforeSchema => return Err(Fail::protocol("a row before the schema")),
            Phase::Done => return Err(self.fail(Fail::protocol("a row after the end"))),
        }
        if cells.len() != self.labels.len() {
            return Err(self.fail(Fail::protocol(format!(
                "row {} has {} cells; the schema has {} columns",
                self.rows + 1,
                cells.len(),
                self.labels.len()
            ))));
        }
        if let Err(f) = self.check(cells) {
            return Err(self.fail(f));
        }
        for (i, cell) in cells.iter().enumerate() {
            if i > 0 {
                self.out.write_str(&self.delimiter)?;
            }
            let text: &str = match cell {
                Cell::Null => &self.options.null_text,
                Cell::Bool(true) => "true",
                Cell::Bool(false) => "false",
                // `check` passed the row: the lexeme is a JSON number and the
                // value is finite, so this pass only formats, once.
                Cell::Number {
                    lexeme: Some(l), ..
                } => l,
                Cell::Number { value, .. } => write_value(*value, &mut self.scratch),
                Cell::String(s) => s,
                Cell::Missing => match &self.options.missing {
                    MissingText::Text(t) => t,
                    // `check` rejected this row already.
                    MissingText::Error => continue,
                },
            };
            self.emitted = true;
            write_field(
                &mut self.out,
                self.options.quoting,
                self.options.delimiter,
                text,
            )?;
        }
        self.emitted = true;
        self.out.write_str(self.options.newline.as_str())?;
        self.rows += 1;
        Ok(())
    }

    /// Reject a row before any of it is written, so a row is rendered whole
    /// or not at all and the output stays a sequence of complete records
    /// whatever the caller does after a failure. Nothing is formatted here;
    /// `row` formats each number once, after the row has passed.
    fn check(&self, cells: &[Cell]) -> Result<(), Fail> {
        for (i, cell) in cells.iter().enumerate() {
            match cell {
                Cell::Number { value, lexeme } => {
                    check_number(*value, lexeme.as_deref()).map_err(|f| {
                        f.at_path(format!(
                            "column {:?}, row {}",
                            self.labels[i],
                            self.rows + 1
                        ))
                    })?;
                }
                Cell::Missing if self.options.missing == MissingText::Error => {
                    return Err(Fail::new(
                        Code::MissingValue,
                        format!(
                            "row {} has no value for column {:?}",
                            self.rows + 1,
                            self.labels[i]
                        ),
                    ));
                }
                _ => {}
            }
        }
        Ok(())
    }

    fn end(&mut self) -> Result<(), Fail> {
        match self.phase {
            Phase::Rows => {}
            Phase::BeforeSchema => return Err(Fail::protocol("the end before the schema")),
            Phase::Done => return Err(self.fail(Fail::protocol("a second end"))),
        }
        // Done only once the flush has succeeded: a table whose last bytes
        // never reached the writer is not done, whatever `End` said.
        self.out.flush()?;
        self.phase = Phase::Done;
        Ok(())
    }
}

impl<O: TextOut> TableSink for CsvRenderer<O> {
    fn table_event(&mut self, ev: TableEvent<'_>) -> Result<Flow, Fail> {
        match ev {
            TableEvent::Schema(columns) => self.schema(columns)?,
            TableEvent::Row(cells) => self.row(cells)?,
            TableEvent::End => self.end()?,
        }
        Ok(Flow::Continue)
    }
}

/// Write one field: quoted with `"` doubled, or bare when the dialect
/// allows and the text needs no quoting.
fn write_field<O: TextOut>(
    out: &mut O,
    quoting: Quoting,
    delimiter: char,
    text: &str,
) -> Result<(), Fail> {
    let quote = match quoting {
        Quoting::Always => true,
        Quoting::Minimal => text
            .chars()
            .any(|c| c == delimiter || matches!(c, '"' | '\r' | '\n')),
    };
    if !quote {
        return out.write_str(text);
    }
    out.write_str("\"")?;
    let mut rest = text;
    while let Some(i) = rest.find('"') {
        // Up to and including the quote, then the quote again: doubled.
        out.write_str(&rest[..=i])?;
        out.write_str("\"")?;
        rest = &rest[i + 1..];
    }
    if !rest.is_empty() {
        out.write_str(rest)?;
    }
    out.write_str("\"")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::text::{StringOut, WriteOut};

    fn cols(labels: &[&str]) -> Vec<PublicColumn> {
        labels.iter().map(|l| PublicColumn::new(*l)).collect()
    }

    fn s(text: &str) -> Cell {
        Cell::String(text.into())
    }

    fn num(lexeme: &str) -> Cell {
        Cell::Number {
            value: lexeme.parse().unwrap_or(0.0),
            lexeme: Some(lexeme.into()),
        }
    }

    /// Render a whole table and give the bytes back.
    fn render(options: CsvOptions, labels: &[&str], rows: &[Vec<Cell>]) -> Result<String, Fail> {
        let mut r = CsvRenderer::new(StringOut::new(), options)?;
        let columns = cols(labels);
        r.table_event(TableEvent::Schema(&columns))?;
        for row in rows {
            r.table_event(TableEvent::Row(row))?;
        }
        r.table_event(TableEvent::End)?;
        Ok(r.into_inner().into_string())
    }

    fn standard(labels: &[&str], rows: &[Vec<Cell>]) -> String {
        render(CsvOptions::default(), labels, rows).unwrap()
    }

    #[test]
    fn every_field_is_quoted_and_every_record_ends_with_crlf() {
        assert_eq!(
            standard(&["name", "age"], &[vec![s("ada"), num("36")]]),
            "\"name\",\"age\"\r\n\"ada\",\"36\"\r\n"
        );
    }

    #[test]
    fn empty_fields_are_an_empty_quote_pair() {
        assert_eq!(
            standard(&["a", "b"], &[vec![s(""), s("")]]),
            "\"a\",\"b\"\r\n\"\",\"\"\r\n"
        );
    }

    #[test]
    fn commas_in_a_field_are_kept_inside_the_quotes() {
        assert_eq!(
            standard(&["a"], &[vec![s("x, y, z")]]),
            "\"a\"\r\n\"x, y, z\"\r\n"
        );
    }

    #[test]
    fn quotes_in_a_field_are_doubled() {
        assert_eq!(
            standard(
                &["a"],
                &[vec![s("say \"hi\"")], vec![s("\"")], vec![s("\"\"")]]
            ),
            "\"a\"\r\n\"say \"\"hi\"\"\"\r\n\"\"\"\"\r\n\"\"\"\"\"\"\r\n"
        );
    }

    #[test]
    fn cr_and_lf_inside_a_field_are_written_as_they_are() {
        assert_eq!(
            standard(&["a"], &[vec![s("line1\r\nline2\nline3\rend")]]),
            "\"a\"\r\n\"line1\r\nline2\nline3\rend\"\r\n"
        );
    }

    #[test]
    fn unicode_passes_through_unchanged() {
        assert_eq!(
            standard(&["名"], &[vec![s("héllo 日本語 🚀")]]),
            "\"名\"\r\n\"héllo 日本語 🚀\"\r\n"
        );
    }

    #[test]
    fn booleans_write_true_and_false() {
        assert_eq!(
            standard(&["a", "b"], &[vec![Cell::Bool(false), Cell::Bool(true)]]),
            "\"a\",\"b\"\r\n\"false\",\"true\"\r\n"
        );
    }

    #[test]
    fn zero_and_other_values_without_a_lexeme_take_the_shortest_form() {
        let row = vec![
            Cell::Number {
                value: 0.0,
                lexeme: None,
            },
            Cell::Number {
                value: 50.25,
                lexeme: None,
            },
            Cell::Number {
                value: 1e20,
                lexeme: None,
            },
            Cell::Number {
                value: 1e21,
                lexeme: None,
            },
            Cell::Number {
                value: 1e-300,
                lexeme: None,
            },
        ];
        assert_eq!(
            standard(&["z", "b", "big", "bigger", "tiny"], &[row]),
            "\"z\",\"b\",\"big\",\"bigger\",\"tiny\"\r\n\"0\",\"50.25\",\"100000000000000000000\",\"1e21\",\"1e-300\"\r\n"
        );
    }

    #[test]
    fn big_lexemes_are_written_verbatim() {
        let row = vec![
            num("123456789012345678901234567890"),
            num("0.1000000000000000055511151231257827"),
            num("-1.5E+308"),
            num("50.250"),
        ];
        assert_eq!(
            standard(&["a", "b", "c", "d"], &[row]),
            "\"a\",\"b\",\"c\",\"d\"\r\n\"123456789012345678901234567890\",\"0.1000000000000000055511151231257827\",\"-1.5E+308\",\"50.250\"\r\n"
        );
    }

    #[test]
    fn a_lexeme_that_is_not_a_json_number_is_invalid_number() {
        for bad in ["1.", "01", "NaN", "0x10", "1_000", ""] {
            let err = render(CsvOptions::default(), &["a"], &[vec![num(bad)]]).unwrap_err();
            assert_eq!(err.code, Code::InvalidNumber, "{bad:?}");
            assert!(err.committed_output, "the header was already written");
        }
    }

    #[test]
    fn nan_and_infinity_are_unrepresentable_with_or_without_a_lexeme() {
        for v in [f64::NAN, f64::INFINITY, f64::NEG_INFINITY] {
            let row = vec![Cell::Number {
                value: v,
                lexeme: None,
            }];
            let err = render(CsvOptions::default(), &["a"], &[row]).unwrap_err();
            assert_eq!(err.code, Code::TargetValueUnrepresentable);
        }
        // The lexeme spells a number, but the value beside it overflowed:
        // the row is refused whole and nothing of it is written.
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let columns = cols(&["a", "b"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let row = [
            s("x"),
            Cell::Number {
                value: f64::INFINITY,
                lexeme: Some("1e999".into()),
            },
        ];
        let err = r.table_event(TableEvent::Row(&row)).unwrap_err();
        assert_eq!(err.code, Code::TargetValueUnrepresentable);
        assert_eq!(err.path.as_deref(), Some("column \"b\", row 1"));
        assert_eq!(r.into_inner().as_str(), "\"a\",\"b\"\r\n");
    }

    #[test]
    fn null_writes_the_null_text_empty_by_default() {
        assert_eq!(
            standard(&["a", "b"], &[vec![Cell::Null, s("x")]]),
            "\"a\",\"b\"\r\n\"\",\"x\"\r\n"
        );
        let options = CsvOptions {
            null_text: "NULL".into(),
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a"], &[vec![Cell::Null]]).unwrap(),
            "\"a\"\r\n\"NULL\"\r\n"
        );
    }

    #[test]
    fn missing_is_an_error_unless_a_text_is_configured() {
        let err = render(
            CsvOptions::default(),
            &["a", "b"],
            &[vec![s("x"), Cell::Missing]],
        )
        .unwrap_err();
        assert_eq!(err.code, Code::MissingValue);
        assert!(err.message.contains("\"b\""));
        assert!(err.committed_output);
        let options = CsvOptions {
            missing: MissingText::Text("N/A".into()),
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a", "b"], &[vec![Cell::Missing, s("x")]]).unwrap(),
            "\"a\",\"b\"\r\n\"N/A\",\"x\"\r\n"
        );
        let options = CsvOptions {
            missing: MissingText::Text("".into()),
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a"], &[vec![Cell::Missing]]).unwrap(),
            "\"a\"\r\n\"\"\r\n"
        );
    }

    #[test]
    fn a_row_that_fails_writes_nothing_so_records_stay_whole() {
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let columns = cols(&["a", "b"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let failing = [
            (vec![s("x"), Cell::Missing], Code::MissingValue),
            (vec![s("x"), num("1.")], Code::InvalidNumber),
            (
                vec![
                    s("x"),
                    Cell::Number {
                        value: f64::NAN,
                        lexeme: None,
                    },
                ],
                Code::TargetValueUnrepresentable,
            ),
        ];
        for (row, code) in &failing {
            let err = r.table_event(TableEvent::Row(row)).unwrap_err();
            assert_eq!(err.code, *code);
            assert_eq!(r.out.as_str(), "\"a\",\"b\"\r\n", "{code}");
        }
        r.table_event(TableEvent::Row(&[s("y"), s("z")])).unwrap();
        r.table_event(TableEvent::End).unwrap();
        assert_eq!(r.rows(), 1);
        assert_eq!(r.into_inner().as_str(), "\"a\",\"b\"\r\n\"y\",\"z\"\r\n");
    }

    #[test]
    fn duplicate_labels_are_allowed() {
        assert_eq!(
            standard(&["a", "a"], &[vec![s("1"), s("2")]]),
            "\"a\",\"a\"\r\n\"1\",\"2\"\r\n"
        );
    }

    #[test]
    fn an_empty_row_sequence_still_writes_the_header() {
        assert_eq!(standard(&["a", "b"], &[]), "\"a\",\"b\"\r\n");
    }

    #[test]
    fn the_header_can_be_turned_off() {
        let options = CsvOptions {
            header: false,
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options.clone(), &["a"], &[vec![s("x")]]).unwrap(),
            "\"x\"\r\n"
        );
        assert_eq!(render(options, &["a"], &[]).unwrap(), "");
    }

    #[test]
    fn the_final_record_ends_with_the_newline_too() {
        let out = standard(&["a"], &[vec![s("1")], vec![s("2")]]);
        assert!(out.ends_with("\"2\"\r\n"));
        assert_eq!(out.matches("\r\n").count(), 3);
    }

    #[test]
    fn lf_is_a_dialect() {
        let options = CsvOptions {
            newline: Newline::Lf,
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a"], &[vec![s("x")]]).unwrap(),
            "\"a\"\n\"x\"\n"
        );
    }

    #[test]
    fn a_tab_delimiter_is_a_dialect() {
        let options = CsvOptions {
            delimiter: '\t',
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a", "b"], &[vec![s("x,y"), s("z")]]).unwrap(),
            "\"a\"\t\"b\"\r\n\"x,y\"\t\"z\"\r\n"
        );
    }

    #[test]
    fn minimal_quoting_quotes_only_what_needs_it() {
        let options = CsvOptions {
            quoting: Quoting::Minimal,
            ..CsvOptions::default()
        };
        let row = vec![
            s("plain"),
            s(""),
            s("a,b"),
            s("say \"hi\""),
            s("x\ny"),
            s("x\ry"),
            num("1.50"),
            Cell::Null,
        ];
        assert_eq!(
            render(
                options,
                &["p", "e", "c", "q", "lf", "cr", "n", "nul"],
                &[row]
            )
            .unwrap(),
            "p,e,c,q,lf,cr,n,nul\r\nplain,,\"a,b\",\"say \"\"hi\"\"\",\"x\ny\",\"x\ry\",1.50,\r\n"
        );
    }

    #[test]
    fn minimal_quoting_quotes_a_field_holding_the_dialects_delimiter() {
        let options = CsvOptions {
            quoting: Quoting::Minimal,
            delimiter: ';',
            ..CsvOptions::default()
        };
        assert_eq!(
            render(options, &["a", "b"], &[vec![s("x;y"), s("x,y")]]).unwrap(),
            "a;b\r\n\"x;y\";x,y\r\n"
        );
    }

    #[test]
    fn the_quote_a_line_break_and_nul_cannot_be_the_delimiter() {
        for bad in ['"', '\r', '\n', '\0'] {
            let options = CsvOptions {
                delimiter: bad,
                ..CsvOptions::default()
            };
            let err = CsvRenderer::new(StringOut::new(), options).err().unwrap();
            assert_eq!(err.code, Code::TargetValueUnrepresentable, "{bad:?}");
        }
        for ok in [',', ';', '\t', '|', ' ', 'x', '→'] {
            let options = CsvOptions {
                delimiter: ok,
                ..CsvOptions::default()
            };
            assert!(
                CsvRenderer::new(StringOut::new(), options).is_ok(),
                "{ok:?}"
            );
        }
    }

    #[test]
    fn zero_columns_is_unrepresentable() {
        let err = render(CsvOptions::default(), &[], &[]).unwrap_err();
        assert_eq!(err.code, Code::TargetValueUnrepresentable);
        assert!(!err.committed_output);
    }

    #[test]
    fn a_row_before_the_schema_is_a_protocol_error() {
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let err = r.table_event(TableEvent::Row(&[s("x")])).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(!err.committed_output);
    }

    #[test]
    fn a_second_schema_is_a_protocol_error() {
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::Schema(&columns)).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(err.committed_output);
    }

    #[test]
    fn a_row_of_the_wrong_width_is_a_protocol_error() {
        for row in [vec![], vec![s("1")], vec![s("1"), s("2"), s("3")]] {
            let err = render(
                CsvOptions::default(),
                &["a", "b"],
                std::slice::from_ref(&row),
            )
            .unwrap_err();
            assert_eq!(err.code, Code::ProtocolOrderError, "{row:?}");
            assert!(err.message.contains("row 1 has"), "{}", err.message);
        }
    }

    #[test]
    fn an_end_before_the_schema_is_a_protocol_error() {
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let err = r.table_event(TableEvent::End).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
    }

    #[test]
    fn events_after_the_end_are_protocol_errors() {
        let mut r = CsvRenderer::new(StringOut::new(), CsvOptions::default()).unwrap();
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        r.table_event(TableEvent::End).unwrap();
        assert!(r.is_done());
        for ev in [
            TableEvent::End,
            TableEvent::Row(&[s("x")]),
            TableEvent::Schema(&columns),
        ] {
            let err = r.table_event(ev).unwrap_err();
            assert_eq!(err.code, Code::ProtocolOrderError);
            assert!(err.committed_output);
        }
    }

    /// A writer that takes every byte and refuses to flush.
    struct NoFlush;

    impl std::io::Write for NoFlush {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            Ok(buf.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Err(std::io::Error::other("pipe closed"))
        }
    }

    #[test]
    fn a_failed_flush_at_end_leaves_the_renderer_not_done() {
        let mut r = CsvRenderer::new(WriteOut::new(NoFlush), CsvOptions::default()).unwrap();
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::End).unwrap_err();
        assert_eq!(err.code, Code::OutputFailed);
        assert!(!r.is_done());
    }

    #[test]
    fn committed_output_means_bytes_that_reached_the_writer() {
        // Buffered in the WriteOut, not yet written: the failure leaves no
        // partial output behind, and says so.
        let mut r = CsvRenderer::new(WriteOut::new(Vec::new()), CsvOptions::default()).unwrap();
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::Schema(&columns)).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(!err.committed_output);
        assert_eq!(r.out.committed(), 0);
        assert!(r.into_inner().into_inner().is_empty());

        // Written through: partial output exists.
        let out = WriteOut::new(Vec::new()).with_budget(0);
        let mut r = CsvRenderer::new(out, CsvOptions::default()).unwrap();
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::Schema(&columns)).unwrap_err();
        assert!(err.committed_output);
        assert_eq!(r.into_inner().into_inner(), b"\"a\"\r\n");
    }

    #[test]
    fn end_flushes_the_output_and_nothing_else_does() {
        let out = WriteOut::new(Vec::new());
        let mut r = CsvRenderer::new(out, CsvOptions::default()).unwrap();
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        r.table_event(TableEvent::Row(&[s("x")])).unwrap();
        assert_eq!(r.out.committed(), 0);
        assert_eq!(r.table_event(TableEvent::End).unwrap(), Flow::Continue);
        assert_eq!(r.rows(), 1);
        assert_eq!(r.out.committed(), 10);
        let bytes = r.into_inner().into_inner();
        assert_eq!(bytes, b"\"a\"\r\n\"x\"\r\n");
    }
}
