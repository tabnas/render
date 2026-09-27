# Reference

The types are documented in the crate (`cargo doc --open` from `rs/`);
this page lists the surface, the contract each piece keeps, and the
decisions taken where transduce's `docs/architecture.md` (section 3, the
design this crate implements) was silent. Every failure code named here is
a `tabnas_transduce::Code`, written as the code is (`PROTOCOL_ORDER_ERROR`).

## Text outputs (`rs/src/text.rs`)

`TextOut`: `write_str(&str) -> Result<(), Fail>` and
`flush() -> Result<(), Fail>`. Fragments are concatenated; where they are
cut carries no meaning. A renderer flushes exactly once, at the end of the
protocol it renders, so a document that failed half way is not flushed as
if it were whole. `&mut O` and `Box<O>` are outputs when `O` is.

`WriteOut<W: io::Write>`: coalesces fragments into a buffer of at most
`budget` bytes (`DEFAULT_BUDGET`, 32 KiB; `with_budget`). A fragment that
would overflow the buffer flushes it first; a fragment at least as large as
the budget goes to the writer directly. `with_limits(&Limits)` enforces
`max_output_bytes`: the check counts buffered and written bytes and runs
BEFORE the fragment is accepted, so the failing fragment is never written
(`RESOURCE_LIMIT_EXCEEDED`, `limit.name = "max_output_bytes"`).
`with_metrics(Arc<Metrics>)` counts `output_bytes` when bytes reach the
writer. A writer error is `OUTPUT_FAILED`; the buffer is not retried.
`committed()` is the bytes the writer received, `accepted()` the bytes
taken in; a failure after any byte was committed carries
`committed_output`. `into_inner()` flushes and hands the writer back, or
fails with the flush's error.

`StringOut`: keeps the text (`as_str`, `into_string`); for tests and small
results.

`Join<O>`: `new(out, separator)`, `item_start()`, `item_end()`. The
separator goes before every item but the first, never between the
fragments of one item; an item with no fragments is still an item, so two
empty items joined with `,` are `,`. A fragment written outside an item is
an item of its own. `item_start` inside an item and `item_end` with none
open are `PROTOCOL_ORDER_ERROR`. `flush` flushes the output beneath and
leaves an open item open.

`ReplaceText<O>`: `new(out, from, to)`. Every occurrence of `from` becomes
`to`, with `str::replace`'s left-to-right non-overlapping matches, however
the text is chunked: at most `from.len() - 1` bytes (the longest suffix
seen that is a proper prefix of `from`) are carried between fragments.
`flush` writes the carry out, so a flush is a boundary no match can span.
An empty `from` matches nothing and the text passes through unchanged.

## CSV (`rs/src/csv.rs`)

`CsvOptions` and its defaults:

| Field | Type | Default |
|---|---|---|
| `delimiter` | `char`, not `"`, CR, LF or NUL | `','` |
| `newline` | `Newline::{Lf, CrLf}` | `CrLf` |
| `header` | `bool` | `true` |
| `null_text` | `Box<str>` | `""` |
| `missing` | `MissingText::{Error, Text(..)}` | `Error` |
| `quoting` | `Quoting::{Always, Minimal}` | `Always` |

`CsvRenderer::new(out, options) -> Result<Self, Fail>` refuses a
delimiter no reader could take (`TARGET_VALUE_UNREPRESENTABLE`). It
implements `TableSink`:

- `Schema` once and first; the header row, when on, writes the labels
  through the same field writer. Zero columns is
  `TARGET_VALUE_UNREPRESENTABLE` (a CSV record cannot be empty). Duplicate
  labels are allowed.
- `Row` exactly as wide as the schema. Cells: `String` writes the text;
  `Bool` writes `true` or `false`; `Null` writes `null_text`; `Number` with
  a lexeme writes it after checking it is a JSON number
  (`INVALID_NUMBER` otherwise), without one writes the shortest text that
  reads back as the same f64, and NaN or infinity without a lexeme is
  `TARGET_VALUE_UNREPRESENTABLE`; `Missing` is `MISSING_VALUE` unless
  `missing` is a `Text`.
- A row is checked before any of it is written, so a row that fails
  (`MISSING_VALUE`, `INVALID_NUMBER`, a non-finite value) writes nothing
  and the output stays a sequence of whole records.
- `End` once, last; it flushes the output. Every record, the header and
  the last row included, ends with the newline.
- A second schema, a row before the schema or after the end, a row of the
  wrong width, an end before the schema or a second end:
  `PROTOCOL_ORDER_ERROR`.

Always quoting writes `"` + the text with `"` doubled + `"`. Minimal
quoting quotes only a field holding the delimiter, `"`, CR or LF, and
writes an empty field as nothing; it is valid here because a row is a
finite vector and the whole field is in hand before it is written.
Quoting is syntax, not protection against spreadsheet formula
interpretation (see AGENTS.md, "Untrusted input").

## JSON (`rs/src/json.rs`)

`JsonOptions { indent: Option<usize>, trailing_newline: bool }`, default
`None` and `false`. `indent: Some(n)` with `n > 0` writes a newline and
`n × depth` spaces before every item and before the closing bracket of a
non-empty container, and `": "` after a key; `None` or `Some(0)` is
compact. Empty containers are `{}` and `[]` in both profiles.

`JsonRenderer::new(out, options)` implements `Sink`. Strings and keys are
escaped by `tabnas_transduce::write_json_string`: `"`, `\`, `\b`, `\f`,
`\n`, `\r`, `\t`, other control characters as `\u00XX`, everything else
(non-ASCII included) as itself. Numbers follow the CSV rules above: a
lexeme is validated (`INVALID_NUMBER`) and written, a value without one
takes the shortest round-trip form, NaN and infinity are
`TARGET_VALUE_UNREPRESENTABLE`. A lexeme is written even when the value
beside it overflowed (`1e999`): the source spelled a number, and its range
is the reader's business.

Exactly one root value, then `End`, which writes the trailing newline if
configured and flushes. `PROTOCOL_ORDER_ERROR`: a second root value, an
end before the root or with a container open, a key outside an object, a
key where a value is due, a value where a key is due, a close with no
matching open (an object end inside an array, an end after a key with no
value, any close at the root), and any event after `End`. Separators are
written when the next item begins, never speculatively, so the text
written before a failure is a prefix of a valid document.

## Records (`rs/src/records.rs`)

`RecordsToJson::new(sink)` and `with_missing(MissingRecord::{Skip, Null,
Error})`, default `Skip`, implements `TableSink` and produces
`JsonEvents/1`: `ArrayStart` at the schema, one `ObjectStart … ObjectEnd`
per row with `Key(label)` and the cell as a scalar event in schema order,
`ArrayEnd` and `End` at `End`. A `Missing` cell is left out (`Skip`),
written as `null` (`Null`) or `MISSING_VALUE` (`Error`, raised before any
of the row is forwarded). A repeated label
is emitted once, in its last column's position, holding the last value.
Protocol validation is the CSV renderer's, except that zero columns is
allowed (an empty object is a JSON value). `Flow::Stop` from the sink
stops the row and propagates.

## Numbers (`rs/src/number.rs`)

`is_json_number(&str)`: RFC 8259's grammar,
`-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`, and nothing else. Both
renderers hold lexemes to it before copying them into the output.

## Codes raised here

`PROTOCOL_ORDER_ERROR`, `TARGET_VALUE_UNREPRESENTABLE`, `INVALID_NUMBER`,
`MISSING_VALUE`, `RESOURCE_LIMIT_EXCEEDED` (`max_output_bytes`),
`OUTPUT_FAILED`. A `Fail` from a renderer carries `committed_output` when
the renderer had written any text before the failure; `RecordsToJson`
sets it when it had forwarded any event, since the stage downstream may
have rendered them.

## Decisions where the design brief was silent

- `WriteOut` counts `output_bytes` when bytes reach the writer, not when
  they are accepted, so the metric means what its name says after a
  failure; `accepted()` is the other number.
- `into_inner` on `WriteOut` flushes first and returns `Result`, so
  unflushed bytes are never dropped silently.
- `Join`: a fragment outside an item is an item; unbalanced markers are
  protocol errors rather than panics.
- `ReplaceText`: an empty literal is the identity; `flush` is a boundary.
- `Quoting::Minimal` writes an empty field as nothing, so an empty string
  and an empty `null_text` read back the same; that is the dialect's
  trade-off, and `Always` is the standard profile for that reason.
- A number lexeme is written whenever it is a JSON number, even if the
  f64 beside it is infinite.
- `JsonOptions { indent: Some(0) }` is compact.
- `RecordsToJson` allows zero columns and resolves repeated labels by
  keeping the last column.
- No `Concat` helper: `Join` with an empty separator, or writing to the
  same `TextOut` in sequence, is concatenation; a type would add nothing.

## Measured

`cargo bench` (`rs/benches/render.rs`), one core of the development
container, 2026-09-27, 20 000 synthetic rows of five columns:

| Group | Throughput |
|---|---|
| `csv_render_rows` (table events → CSV over a discarding writer) | about 4.3 M rows/s |
| `csv_render_bytes` (the same, in output bytes) | about 290 MiB/s |
| `json_render` (parsed 20k-record document → `ValueSource` → compact JSON) | about 78 MiB/s of source |

The renderers are not the bottleneck of an export: the engine parses at
about 1 MB/s (transduce's `docs/BENCH.md`). Re-measure before quoting.
