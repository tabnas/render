//! `TableRows/1` as `JsonEvents/1`: an array of records keyed by label.
//!
//! A table has a natural JSON form, one object per row with the column
//! labels as member names, and producing it as events rather than text
//! means the JSON renderer, and every other `JsonEvents/1` consumer, gets
//! it for free. The stage retains the labels and nothing else: each row is
//! emitted as it arrives and forgotten. Labels are data from the source's
//! metadata, so a repeated label is not an error here (the CSV renderer
//! allows it too); it is resolved the way a JSON reader would resolve a
//! repeated member in the un-deduplicated record, by keeping the last
//! value that is there, and the output then carries each label once.

use tabnas_alchemy::shared::{
    Cell, Code, Fail, Flow, JsonEvent, Number, PublicColumn, Sink, TableEvent, TableSink,
};

/// What a [`Cell::Missing`] becomes in a record.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum MissingRecord {
    /// Leave the member out: the record says nothing where the source had
    /// nothing, which is what an absent path meant.
    #[default]
    Skip,
    /// Write the member with a `null` value.
    Null,
    /// Fail the run with `MISSING_VALUE`.
    Error,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Phase {
    BeforeSchema,
    Rows,
    Done,
}

/// Turns `TableRows/1` into `JsonEvents/1`.
///
/// `Schema` opens the array, each `Row` is one object whose members are the
/// labels in schema order, `End` closes the array and ends the document.
/// The protocol is validated as the CSV renderer validates it: one schema
/// first, rows of the schema's width, one end, `PROTOCOL_ORDER_ERROR`
/// otherwise. A schema with no columns is allowed, since an empty object
/// is a JSON value; the CSV renderer's refusal is about CSV. When a label
/// repeats, each record carries it once, from the last column whose cell
/// contributes a member (under `Skip` a `Missing` cell contributes none),
/// in that column's position: the value a reader of the un-deduplicated
/// record would keep, since a member that was never written cannot win.
/// A failure found after events were forwarded is marked as having
/// committed output, since the stage downstream may have rendered them.
pub struct RecordsToJson<S: Sink> {
    sink: S,
    missing: MissingRecord,
    phase: Phase,
    labels: Vec<Box<str>>,
    /// Per column, the next later column with the same label, so a row
    /// can find the column that carries the label's value; `None` for the
    /// common case of a label that does not repeat.
    next_same: Vec<Option<usize>>,
    rows: u64,
    forwarded: bool,
}

/// Whether a cell contributes a member to its record under this policy:
/// every cell but a `Missing` that is skipped.
fn contributes(cell: &Cell, missing: MissingRecord) -> bool {
    !(cell.is_missing() && missing == MissingRecord::Skip)
}

impl<S: Sink> RecordsToJson<S> {
    pub fn new(sink: S) -> Self {
        RecordsToJson {
            sink,
            missing: MissingRecord::Skip,
            phase: Phase::BeforeSchema,
            labels: Vec::new(),
            next_same: Vec::new(),
            rows: 0,
            forwarded: false,
        }
    }

    pub fn with_missing(mut self, missing: MissingRecord) -> Self {
        self.missing = missing;
        self
    }

    /// Rows emitted so far.
    pub fn rows(&self) -> u64 {
        self.rows
    }

    /// Whether `End` has been forwarded.
    pub fn is_done(&self) -> bool {
        self.phase == Phase::Done
    }

    pub fn into_inner(self) -> S {
        self.sink
    }

    fn fail(&self, f: Fail) -> Fail {
        if self.forwarded {
            f.committed()
        } else {
            f
        }
    }

    fn send(&mut self, ev: JsonEvent<'_>) -> Result<Flow, Fail> {
        self.forwarded = true;
        self.sink.event(ev)
    }

    fn schema(&mut self, columns: &[PublicColumn]) -> Result<Flow, Fail> {
        match self.phase {
            Phase::BeforeSchema => {}
            Phase::Rows => return Err(self.fail(Fail::protocol("a second schema"))),
            Phase::Done => return Err(self.fail(Fail::protocol("a schema after the end"))),
        }
        self.labels = columns.iter().map(|c| c.label.clone()).collect();
        self.next_same = (0..self.labels.len())
            .map(|i| (i + 1..self.labels.len()).find(|&j| self.labels[j] == self.labels[i]))
            .collect();
        self.phase = Phase::Rows;
        self.send(JsonEvent::ArrayStart)
    }

    fn row(&mut self, cells: &[Cell]) -> Result<Flow, Fail> {
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
        if self.missing == MissingRecord::Error {
            // Before any of the row is forwarded, so a row is emitted whole
            // or not at all, as the CSV renderer renders it.
            if let Some(i) = cells.iter().position(Cell::is_missing) {
                return Err(self.fail(Fail::new(
                    Code::MissingValue,
                    format!(
                        "row {} has no value for column {:?}",
                        self.rows + 1,
                        self.labels[i]
                    ),
                )));
            }
        }
        // The sink copies what it keeps, as the protocol says, so a key is
        // the label borrowed and the row costs no allocation. The fields
        // are borrowed apart from one another for that: the sink and the
        // forwarded flag mutably for the sends, the labels for the keys.
        let RecordsToJson {
            sink,
            missing,
            labels,
            next_same,
            rows,
            forwarded,
            ..
        } = self;
        let mut send = |ev: JsonEvent<'_>| -> Result<Flow, Fail> {
            *forwarded = true;
            sink.event(ev)
        };
        macro_rules! send {
            ($ev:expr) => {
                if send($ev)? == Flow::Stop {
                    return Ok(Flow::Stop);
                }
            };
        }
        send!(JsonEvent::ObjectStart);
        for (i, cell) in cells.iter().enumerate() {
            if !contributes(cell, *missing) {
                continue;
            }
            // A repeated label is written from the last column whose cell
            // contributes; an earlier column's value is superseded only by
            // a member that will actually be there.
            let mut later = next_same.get(i).copied().flatten();
            while let Some(j) = later {
                if cells.get(j).is_some_and(|c| contributes(c, *missing)) {
                    break;
                }
                later = next_same.get(j).copied().flatten();
            }
            if later.is_some() {
                continue;
            }
            let value = match cell {
                Cell::Null => JsonEvent::Null,
                Cell::Bool(b) => JsonEvent::Bool(*b),
                Cell::Number { value, lexeme } => JsonEvent::Number(Number {
                    value: *value,
                    lexeme: lexeme.as_deref(),
                }),
                Cell::String(s) => JsonEvent::String(s),
                Cell::Missing => match *missing {
                    MissingRecord::Null => JsonEvent::Null,
                    // `Skip` does not contribute and was passed over above;
                    // `Error` was rejected before the row began.
                    MissingRecord::Skip | MissingRecord::Error => continue,
                },
            };
            send!(JsonEvent::Key(&labels[i]));
            send!(value);
        }
        send!(JsonEvent::ObjectEnd);
        *rows += 1;
        Ok(Flow::Continue)
    }

    fn end(&mut self) -> Result<Flow, Fail> {
        match self.phase {
            Phase::Rows => {}
            Phase::BeforeSchema => return Err(Fail::protocol("the end before the schema")),
            Phase::Done => return Err(self.fail(Fail::protocol("a second end"))),
        }
        if self.send(JsonEvent::ArrayEnd)? == Flow::Stop {
            return Ok(Flow::Stop);
        }
        // Done only once `End` has been taken downstream: a sink that
        // failed on it has not seen the document end.
        let flow = self.send(JsonEvent::End)?;
        self.phase = Phase::Done;
        Ok(flow)
    }
}

impl<S: Sink> TableSink for RecordsToJson<S> {
    fn table_event(&mut self, ev: TableEvent<'_>) -> Result<Flow, Fail> {
        match ev {
            TableEvent::Schema(columns) => self.schema(columns),
            TableEvent::Row(cells) => self.row(cells),
            TableEvent::End => self.end(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::json::{JsonOptions, JsonRenderer};
    use crate::text::StringOut;
    use tabnas_alchemy::shared::{FnSink, OwnedJsonEvent};
    use OwnedJsonEvent::*;

    fn cols(labels: &[&str]) -> Vec<PublicColumn> {
        labels.iter().map(|l| PublicColumn::new(*l)).collect()
    }

    fn s(text: &str) -> Cell {
        Cell::String(text.into())
    }

    fn key(k: &str) -> OwnedJsonEvent {
        Key(k.into())
    }

    fn str(v: &str) -> OwnedJsonEvent {
        String(v.into())
    }

    fn run(
        missing: MissingRecord,
        labels: &[&str],
        rows: &[Vec<Cell>],
    ) -> Result<Vec<OwnedJsonEvent>, Fail> {
        let mut r = RecordsToJson::new(Vec::new()).with_missing(missing);
        let columns = cols(labels);
        r.table_event(TableEvent::Schema(&columns))?;
        for row in rows {
            r.table_event(TableEvent::Row(row))?;
        }
        r.table_event(TableEvent::End)?;
        assert!(r.is_done());
        Ok(r.into_inner())
    }

    #[test]
    fn a_table_becomes_an_array_of_objects_keyed_by_label() {
        let rows = vec![
            vec![
                Cell::Number {
                    value: 1.0,
                    lexeme: Some("1.0".into()),
                },
                s("ada"),
                Cell::Bool(true),
            ],
            vec![
                Cell::Number {
                    value: 2.0,
                    lexeme: None,
                },
                Cell::Null,
                Cell::Bool(false),
            ],
        ];
        assert_eq!(
            run(MissingRecord::Skip, &["id", "name", "ok"], &rows).unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("id"),
                OwnedJsonEvent::Number {
                    value: 1.0,
                    lexeme: Some("1.0".into())
                },
                key("name"),
                str("ada"),
                key("ok"),
                Bool(true),
                ObjectEnd,
                ObjectStart,
                key("id"),
                OwnedJsonEvent::Number {
                    value: 2.0,
                    lexeme: None
                },
                key("name"),
                Null,
                key("ok"),
                Bool(false),
                ObjectEnd,
                ArrayEnd,
                End,
            ]
        );
    }

    #[test]
    fn no_rows_is_an_empty_array() {
        assert_eq!(
            run(MissingRecord::Skip, &["a"], &[]).unwrap(),
            vec![ArrayStart, ArrayEnd, End]
        );
    }

    #[test]
    fn zero_columns_gives_empty_objects() {
        assert_eq!(
            run(MissingRecord::Skip, &[], &[vec![], vec![]]).unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                ObjectEnd,
                ObjectStart,
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
    }

    #[test]
    fn a_missing_cell_is_skipped_by_default() {
        assert_eq!(
            run(
                MissingRecord::Skip,
                &["a", "b"],
                &[vec![Cell::Missing, s("x")]]
            )
            .unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("b"),
                str("x"),
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
    }

    #[test]
    fn a_missing_cell_can_be_null_or_an_error() {
        assert_eq!(
            run(
                MissingRecord::Null,
                &["a", "b"],
                &[vec![Cell::Missing, s("x")]]
            )
            .unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("a"),
                Null,
                key("b"),
                str("x"),
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
        let mut r = RecordsToJson::new(Vec::new()).with_missing(MissingRecord::Error);
        let columns = cols(&["a", "b"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r
            .table_event(TableEvent::Row(&[s("x"), Cell::Missing]))
            .unwrap_err();
        assert_eq!(err.code, Code::MissingValue);
        assert!(err.message.contains("\"b\""));
        assert!(err.committed_output, "the array start was forwarded");
        assert_eq!(r.into_inner(), vec![ArrayStart], "nothing of the row was");
    }

    #[test]
    fn a_repeated_label_keeps_the_last_value_that_is_present() {
        // Row two's last "a" is Missing and skipped, so the reader of the
        // un-deduplicated record would keep "4"; row three has no "a" at
        // all. Under `Null` the Missing member is there, and wins.
        let rows = vec![
            vec![s("1"), s("2"), s("3")],
            vec![s("4"), s("5"), Cell::Missing],
            vec![Cell::Missing, s("6"), Cell::Missing],
        ];
        assert_eq!(
            run(MissingRecord::Skip, &["a", "b", "a"], &rows).unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("b"),
                str("2"),
                key("a"),
                str("3"),
                ObjectEnd,
                ObjectStart,
                key("a"),
                str("4"),
                key("b"),
                str("5"),
                ObjectEnd,
                ObjectStart,
                key("b"),
                str("6"),
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
        assert_eq!(
            run(MissingRecord::Null, &["a", "b", "a"], &rows[1..2]).unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("b"),
                str("5"),
                key("a"),
                Null,
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
        // Three columns with one label: the middle one wins when the last
        // is absent.
        assert_eq!(
            run(
                MissingRecord::Skip,
                &["a", "a", "a"],
                &[vec![s("1"), s("2"), Cell::Missing]]
            )
            .unwrap(),
            vec![
                ArrayStart,
                ObjectStart,
                key("a"),
                str("2"),
                ObjectEnd,
                ArrayEnd,
                End
            ]
        );
    }

    #[test]
    fn protocol_errors_match_the_csv_renderers() {
        let columns = cols(&["a"]);

        let mut r = RecordsToJson::new(Vec::new());
        let err = r.table_event(TableEvent::Row(&[s("x")])).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(!err.committed_output);
        let err = r.table_event(TableEvent::End).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);

        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::Schema(&columns)).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(err.committed_output);
        let err = r
            .table_event(TableEvent::Row(&[s("x"), s("y")]))
            .unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);
        assert!(err.message.contains("row 1 has 2 cells"));
        let err = r.table_event(TableEvent::Row(&[])).unwrap_err();
        assert_eq!(err.code, Code::ProtocolOrderError);

        r.table_event(TableEvent::End).unwrap();
        for ev in [
            TableEvent::End,
            TableEvent::Row(&[s("x")]),
            TableEvent::Schema(&columns),
        ] {
            let err = r.table_event(ev).unwrap_err();
            assert_eq!(err.code, Code::ProtocolOrderError);
            assert!(err.committed_output);
        }
        assert_eq!(r.rows(), 0);
        assert_eq!(r.into_inner(), vec![ArrayStart, ArrayEnd, End]);
    }

    #[test]
    fn a_sink_that_fails_on_end_leaves_the_stage_not_done() {
        let sink = FnSink(|ev: JsonEvent<'_>| {
            if let JsonEvent::End = ev {
                Err(Fail::output("closed"))
            } else {
                Ok(Flow::Continue)
            }
        });
        let mut r = RecordsToJson::new(sink);
        let columns = cols(&["a"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let err = r.table_event(TableEvent::End).unwrap_err();
        assert_eq!(err.code, Code::OutputFailed);
        assert!(!r.is_done());
    }

    #[test]
    fn keys_borrow_the_schemas_labels_rather_than_copying_them_per_cell() {
        // Two live allocations cannot share an address, so a key that is a
        // per-cell copy of the label would point elsewhere than the label
        // the stage retains; the same address on every row proves the
        // borrow (and, with it, the absence of the allocation).
        let mut seen: Vec<usize> = Vec::new();
        let sink = FnSink(|ev: JsonEvent<'_>| {
            if let JsonEvent::Key(k) = ev {
                seen.push(k.as_ptr() as usize);
            }
            Ok(Flow::Continue)
        });
        let mut r = RecordsToJson::new(sink);
        let columns = cols(&["first", "second"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        let row = [s("x"), Cell::Null];
        r.table_event(TableEvent::Row(&row)).unwrap();
        r.table_event(TableEvent::Row(&row)).unwrap();
        let labels: Vec<usize> = r.labels.iter().map(|l| l.as_ptr() as usize).collect();
        drop(r);
        assert_eq!(seen, [labels.clone(), labels].concat());
    }

    #[test]
    fn a_stop_from_the_sink_stops_the_row() {
        let mut seen = 0;
        let sink = FnSink(|_ev: JsonEvent<'_>| {
            seen += 1;
            Ok(if seen == 3 {
                Flow::Stop
            } else {
                Flow::Continue
            })
        });
        let mut r = RecordsToJson::new(sink);
        let columns = cols(&["a", "b"]);
        assert_eq!(
            r.table_event(TableEvent::Schema(&columns)).unwrap(),
            Flow::Continue
        );
        assert_eq!(
            r.table_event(TableEvent::Row(&[s("x"), s("y")])).unwrap(),
            Flow::Stop
        );
        drop(r);
        assert_eq!(
            seen, 3,
            "ArrayStart, ObjectStart, the first key, then no more"
        );
    }

    #[test]
    fn records_render_as_json_text() {
        let renderer = JsonRenderer::new(StringOut::new(), JsonOptions::default());
        let mut r = RecordsToJson::new(renderer);
        let columns = cols(&["name", "age"]);
        r.table_event(TableEvent::Schema(&columns)).unwrap();
        r.table_event(TableEvent::Row(&[
            s("ada"),
            Cell::Number {
                value: 36.0,
                lexeme: Some("36".into()),
            },
        ]))
        .unwrap();
        r.table_event(TableEvent::Row(&[s("lin"), Cell::Missing]))
            .unwrap();
        r.table_event(TableEvent::End).unwrap();
        assert_eq!(r.rows(), 2);
        assert_eq!(
            r.into_inner().into_inner().as_str(),
            r#"[{"name":"ada","age":36},{"name":"lin"}]"#
        );
    }
}
