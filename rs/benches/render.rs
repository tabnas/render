//! Throughput of the renderers on synthetic input. Run with `cargo bench`;
//! each group prints a line before it starts, so a long run is never
//! silent, and the numbers belong next to the engine's in transduce's
//! `docs/BENCH.md` when they are quoted.
//!
//! Two questions: how fast does the CSV renderer turn table events into
//! bytes when nothing upstream slows it (rows per second and bytes per
//! second, over a discarding writer), and how fast does a parsed document
//! flow through the whole-value walker into the JSON renderer.

use std::io;

use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use tabnas_render::{CsvOptions, CsvRenderer, JsonOptions, JsonRenderer, TextOut, WriteOut};
use tabnas_transduce::{Cell, PublicColumn, Source, TableEvent, TableSink, ValueSource};

const ROWS: usize = 20_000;

fn columns() -> Vec<PublicColumn> {
    ["id", "name", "balance", "active", "note"]
        .iter()
        .map(|l| PublicColumn::new(*l))
        .collect()
}

/// Rows with the mix a real export has: numbers with lexemes, short and
/// medium strings (some needing `"` doubled), booleans and nulls.
fn synthetic_rows(n: usize) -> Vec<Vec<Cell>> {
    (0..n)
        .map(|i| {
            let balance = format!("{}.{:02}", i * 7, i % 100);
            vec![
                Cell::Number {
                    value: i as f64,
                    lexeme: Some(i.to_string().into()),
                },
                Cell::String(format!("Person number {i}").into()),
                Cell::Number {
                    value: balance.parse().unwrap_or(0.0),
                    lexeme: Some(balance.into()),
                },
                Cell::Bool(i % 3 == 0),
                if i % 5 == 0 {
                    Cell::Null
                } else {
                    Cell::String(format!("note \"{i}\", with a comma").into())
                },
            ]
        })
        .collect()
}

/// Render the table to `out` and hand `out` back with what it counted.
fn render_csv<O: TextOut>(out: O, columns: &[PublicColumn], rows: &[Vec<Cell>]) -> O {
    let mut r =
        CsvRenderer::new(out, CsvOptions::default()).expect("the default delimiter is valid");
    r.table_event(TableEvent::Schema(columns))
        .expect("one schema");
    for row in rows {
        r.table_event(TableEvent::Row(row))
            .expect("rows of the schema's width");
    }
    r.table_event(TableEvent::End).expect("one end");
    r.into_inner()
}

fn csv_render(c: &mut Criterion) {
    let columns = columns();
    let rows = synthetic_rows(ROWS);
    let bytes = render_csv(WriteOut::new(io::sink()), &columns, &rows).committed();
    println!("bench csv_render: {ROWS} rows, {bytes} bytes of CSV per iteration");

    let mut group = c.benchmark_group("csv_render_rows");
    group.throughput(Throughput::Elements(ROWS as u64));
    group.bench_function("rows_per_second", |b| {
        b.iter(|| render_csv(WriteOut::new(io::sink()), &columns, &rows).committed())
    });
    group.finish();

    println!("bench csv_render: the same table, measured in bytes");
    let mut group = c.benchmark_group("csv_render_bytes");
    group.throughput(Throughput::Bytes(bytes));
    group.bench_function("bytes_per_second", |b| {
        b.iter(|| render_csv(WriteOut::new(io::sink()), &columns, &rows).committed())
    });
    group.finish();
}

/// The spec's worked example shape, `records` records long.
fn records_json(records: usize) -> String {
    let mut s = String::from(
        r#"{"response":{"metadata":{"fields":[{"title":"Identifier","path":["id"]},{"title":"Full name","path":["person","name"]},{"title":"Balance","path":["account","balance"]}]},"payload":{"deep":{"records":["#,
    );
    for i in 0..records {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&format!(
            r#"{{"id":{i},"person":{{"name":"Person number {i}"}},"account":{{"balance":{}.{:02}}}}}"#,
            i * 7,
            i % 100
        ));
    }
    s.push_str("]}}}}");
    s
}

fn json_render(c: &mut Criterion) {
    let src = records_json(ROWS);
    println!(
        "bench json_render: parsing {ROWS} records ({} bytes) once",
        src.len()
    );
    let value = tabnas_json::parse(&src).expect("the generated document parses");
    println!("bench json_render: walking the parsed value into the JSON renderer");
    let mut group = c.benchmark_group("json_render");
    group.throughput(Throughput::Bytes(src.len() as u64));
    group.bench_function("value_source_to_compact_json", |b| {
        b.iter(|| {
            let mut r = JsonRenderer::new(WriteOut::new(io::sink()), JsonOptions::default());
            ValueSource(&value)
                .run(&mut r)
                .expect("a parsed value renders");
            r.into_inner().committed()
        })
    });
    group.finish();
}

criterion_group!(benches, csv_render, json_render);
criterion_main!(benches);
