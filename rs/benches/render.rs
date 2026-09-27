//! Throughput of the renderers on synthetic input. Run with `cargo bench`;
//! each group prints a line before it starts, so a long run is never
//! silent, and the numbers belong next to the engine's in transduce's
//! `docs/BENCH.md` when they are quoted.
//!
//! Two kinds of question. RENDERER ONLY: how fast does the CSV renderer
//! turn table events into bytes when nothing upstream slows it
//! (`csv_render_rows`, `csv_render_bytes`: pre-built cells over a
//! discarding writer), and how fast does an already parsed document flow
//! through the whole-value walker into the JSON renderer (`json_render`).
//! END TO END: how fast does JSON text become CSV through the chain the
//! design brief assigns to this crate, parser to rule events to
//! `TableFromJson` to `CsvRenderer` to `WriteOut` (`json_to_csv`), which
//! is the number the brief's sixth target asks for; the same chain from the
//! parsed value separates the parse from the stages after it.

use std::io;

use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use tabnas_render::{CsvOptions, CsvRenderer, JsonOptions, JsonRenderer, TextOut, WriteOut};
use tabnas_transduce::{
    column_from_meta, Cell, Duplicates, Limits, Metrics, ParserSource, Prune, PublicColumn, Schema,
    Selector, Source, SourceMode, TableBinding, TableEvent, TableFromJson, TableSink, ValueSource,
};

/// 20k records in a release measurement; under `cargo test` (a debug build
/// running each bench once) a tenth of that keeps the gate quick, since the
/// engine parses at a fraction of its release speed there.
const ROWS: usize = if cfg!(debug_assertions) {
    2_000
} else {
    20_000
};

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
    println!(
        "bench csv_render (renderer only): {ROWS} pre-built rows, {bytes} bytes of CSV per iteration"
    );

    let mut group = c.benchmark_group("csv_render_rows");
    group.throughput(Throughput::Elements(ROWS as u64));
    group.bench_function("rows_per_second", |b| {
        b.iter(|| render_csv(WriteOut::new(io::sink()), &columns, &rows).committed())
    });
    group.finish();

    println!("bench csv_render (renderer only): the same table, measured in bytes");
    let mut group = c.benchmark_group("csv_render_bytes");
    group.throughput(Throughput::Bytes(bytes));
    group.bench_function("bytes_per_second", |b| {
        b.iter(|| render_csv(WriteOut::new(io::sink()), &columns, &rows).committed())
    });
    group.finish();
}

/// The spec's worked example shape, `records` records long: three columns
/// declared by path under `response.metadata.fields`, the records under
/// `response.payload.deep.records`.
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
        "bench json_render (walk and renderer, no parse): parsing {ROWS} records ({} bytes) once",
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

/// Where the worked example's records are.
fn records_selector() -> Selector {
    Selector::root()
        .property("response")
        .property("payload")
        .property("deep")
        .property("records")
        .each_index()
}

/// The spec's `api-binding`: columns from the document's metadata, rows
/// from the records.
fn binding() -> TableBinding {
    TableBinding {
        schema: Schema::FromMetadata {
            columns: Selector::root()
                .property("response")
                .property("metadata")
                .property("fields"),
            column: Box::new(column_from_meta),
        },
        rows: records_selector(),
    }
}

/// The stages after the source: the table transducer over the CSV renderer
/// over a coalescing writer that discards, so the chain is measured and the
/// disk is not.
fn csv_chain() -> TableFromJson<CsvRenderer<WriteOut<io::Sink>>> {
    let csv = CsvRenderer::new(WriteOut::new(io::sink()), CsvOptions::default())
        .expect("the default delimiter is valid");
    TableFromJson::new(
        binding(),
        &Limits::default(),
        Duplicates::LastWins,
        Metrics::new(),
        csv,
    )
    .expect("the worked-example binding is valid")
}

/// The incremental source over the JSON grammar, pruning each streamed
/// record from the tree as aless runs it.
fn incremental(src: &str) -> ParserSource<'_> {
    ParserSource::new(tabnas_json::make(), src).mode(SourceMode::Incremental {
        prune: Prune::Under(records_selector()),
    })
}

fn json_to_csv(c: &mut Criterion) {
    let src = records_json(ROWS);
    // Once outside the measurement: the chain ends, every record is a row,
    // and the output size is known.
    let (outcome, table) = incremental(&src).run_owned(csv_chain());
    outcome.expect("the chain runs");
    let csv = table.into_inner();
    let rows = csv.rows();
    let bytes = csv.into_inner().committed();
    assert_eq!(rows, ROWS as u64, "every record is a row");
    println!(
        "bench json_to_csv (end to end): {ROWS} records, {} bytes of JSON in, {bytes} bytes of CSV out per iteration",
        src.len()
    );
    let mut group = c.benchmark_group("json_to_csv");
    group.sample_size(10);
    group.throughput(Throughput::Bytes(src.len() as u64));
    group.bench_function("text_to_csv_incremental", |b| {
        b.iter(|| {
            let (outcome, table) = incremental(&src).run_owned(csv_chain());
            outcome.expect("the chain runs");
            table.into_inner().into_inner().committed()
        })
    });

    println!(
        "bench json_to_csv: the same chain from the parsed value (the stages after the parse)"
    );
    let value = tabnas_json::parse(&src).expect("the generated document parses");
    group.bench_function("parsed_value_to_csv", |b| {
        b.iter(|| {
            let mut table = csv_chain();
            ValueSource(&value).run(&mut table).expect("the chain runs");
            table.into_inner().into_inner().committed()
        })
    });
    group.finish();
}

criterion_group!(benches, csv_render, json_render, json_to_csv);
criterion_main!(benches);
