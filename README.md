# tabnas-render

Renderers for the [tabnas-transduce](https://github.com/tabnas/transduce)
protocols: streamed tables become CSV, streamed events become JSON,
incrementally and under backpressure. A renderer decides how a semantic
protocol becomes text and never interprets the source; that separation is
the point.

```text
TableRows/1 ──▶ CsvRenderer ──▶ text     JsonEvents/1 ──▶ JsonRenderer ──▶ text
TableRows/1 ──▶ RecordsToJson ──▶ JsonEvents/1
```

- **Always-quoted CSV** (the standard profile): every field quoted, `"`
  doubled, CRLF line endings, exact number lexemes, explicit null and
  missing policies. Other delimiters, LF and minimal quoting are an
  explicitly selected dialect.
- **Compact JSON** with RFC 8259 escaping and lexemes kept; fixed-indent
  pretty printing as a separate profile.
- **Validated as it goes**: one schema, rows of the schema's width, one
  end, one root value; protocol errors carry the stable codes every stage
  shares, and say whether output had already been written.
- **Bounded output**: fragments are coalesced to a byte budget (32 KiB by
  default) before they reach the writer, `max_output_bytes` fails before
  the fragment that would exceed it, and `output_bytes` is counted into
  the shared metrics.

Rust only for now. The language that composes transducers and renderers
is [tabnas-alchemy](https://github.com/tabnas/alchemy);
[aless](https://github.com/rjrodger/aless) exposes both from the command
line.

## Use

A table arrives as `TableRows/1` events and leaves as CSV bytes; this
example (a doctest, so it is kept true) renders two rows to a `Vec<u8>`
through the coalescing writer:

```rust
use tabnas_render::{CsvOptions, CsvRenderer, WriteOut};
use tabnas_transduce::{Cell, PublicColumn, TableEvent, TableSink};

let columns = [PublicColumn::new("name"), PublicColumn::new("balance")];
let mut csv = CsvRenderer::new(WriteOut::new(Vec::new()), CsvOptions::default())?;
csv.table_event(TableEvent::Schema(&columns))?;
csv.table_event(TableEvent::Row(&[
    Cell::String("Ada, \"the\" first".into()),
    Cell::Number { value: 50.25, lexeme: Some("50.250".into()) },
]))?;
csv.table_event(TableEvent::Row(&[Cell::Null, Cell::Bool(false)]))?;
csv.table_event(TableEvent::End)?;   // flushes the writer

let bytes = csv.into_inner().into_inner();   // hands the Vec back; never flushes
assert_eq!(
    String::from_utf8(bytes).unwrap(),
    "\"name\",\"balance\"\r\n\"Ada, \"\"the\"\" first\",\"50.250\"\r\n\"\",\"false\"\r\n"
);
# Ok::<(), tabnas_transduce::Fail>(())
```

`JsonRenderer` is a `tabnas_transduce::Sink` and takes `JsonEvents/1` the
same way; `RecordsToJson` sits between a table and a `Sink` to write the
table as an array of objects.

## Layout

| Path | What it is |
|---|---|
| [`rs/`](rs/) | the `tabnas-render` crate (library `tabnas_render`) |
| [`docs/reference.md`](docs/reference.md) | the options, the contracts, the codes raised and the decisions taken; the design lives in transduce's `docs/architecture.md`, section 3 |
| [`ci/rust/run.sh`](ci/rust/run.sh) | the gate CI runs |

## Build and test

Sibling checkouts (`../transduce`, `../parser`, the grammars) are named by
path in `rs/Cargo.toml`.

```bash
make build
make test
make bench     # criterion; prints a line per group
```

Contributors and agents: read [`AGENTS.md`](AGENTS.md).

## License

MIT. Copyright (c) Richard Rodger.
