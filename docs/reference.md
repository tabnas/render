# Reference

The types are documented in the crate (`cargo doc --open` from `rs/`);
this page lists the surface, the contract each piece keeps, and the
decisions taken where transduce's `docs/architecture.md` (section 3, the
design this crate implements) was silent. Every failure code named here is
a `tabnas_transduce::Code`, written as the code is (`PROTOCOL_ORDER_ERROR`).

## Text outputs (`rs/src/text.rs`)

`TextOut`: `write_str(&str) -> Result<(), Fail>`,
`flush() -> Result<(), Fail>` and `has_committed() -> bool`. Fragments are
concatenated; where they are cut carries no meaning. A renderer flushes
exactly once, at the end of the protocol it renders, so a document that
failed half way is not flushed as if it were whole. `has_committed` says
whether any text has reached the final destination; a renderer that fails
reports `committed_output` from it. The default answer is `true`, the
conservative one for an output that cannot tell; `WriteOut` answers from
the bytes its writer received, `StringOut` from whether it holds text,
`Join` and `ReplaceText` ask the output beneath. `&mut O` and `Box<O>`
are outputs when `O` is.

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
`committed_output`. `into_inner()` hands the writer back WITHOUT flushing:
what was buffered and never flushed is dropped, so the writer holds
exactly the bytes `committed()` counts, and a document that failed before
its `End` does not reach the writer on the way out. A caller that wants
the partial output anyway calls `flush()` first.

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
  reads back as the same f64 (see Numbers below), and NaN or infinity is
  `TARGET_VALUE_UNREPRESENTABLE` with or without a lexeme; `Missing` is
  `MISSING_VALUE` unless `missing` is a `Text`.
- A row is checked before any of it is written, so a row that fails
  (`MISSING_VALUE`, `INVALID_NUMBER`, a non-finite value) writes nothing
  and the output stays a sequence of whole records. The check formats
  nothing; each number is formatted once, when the row is written.
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
escaped as RFC 8259 requires and no more: `"`, `\`, `\b`, `\f`, `\n`,
`\r`, `\t`, other control characters as `\u00XX`, everything else
(non-ASCII included) as itself; the tests hold the escaping to
`tabnas_transduce::write_json_string`. The escaped text is streamed, run by
run and escape by escape, so the renderer never holds a copy of a string,
however long. Numbers follow the CSV rules above: a
lexeme is validated (`INVALID_NUMBER`) and written, a value without one
takes the shortest form described under Numbers, and NaN and infinity are
`TARGET_VALUE_UNREPRESENTABLE`, lexeme or not: `1e999` spells a number,
but the value the pipeline holds is infinity and a JSON reader given the
text refuses it as out of range (the crate's own oracle, serde_json,
does). A number is checked before its separator is written.

Exactly one root value, then `End`, which writes the trailing newline if
configured and flushes. `PROTOCOL_ORDER_ERROR`: a second root value, an
end before the root or with a container open, a key outside an object, a
key where a value is due, a value where a key is due, a close with no
matching open (an object end inside an array, an end after a key with no
value, any close at the root), and any event after `End`. Separators are
written when the next item begins, never speculatively, and a number is
validated before its separator, so the text written before a failure is a
prefix of a valid document and a caller that carries on after a rejected
value does not find `[1,,2]`.

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

Both renderers check a number before writing anything for it: a lexeme
that is not a JSON number is `INVALID_NUMBER`; a value that is not finite
is `TARGET_VALUE_UNREPRESENTABLE`, with or without a lexeme. The check
formats nothing.

A number without a lexeme is written as the shortest digit string that
reads back as the same f64 (Rust's float formatting), laid out
positionally when the magnitude is zero or within `[1e-6, 1e21)` and in
exponent form (`1e300`, `-2.5e-8`) outside. Those are the thresholds
JavaScript's `Number#toString` uses, so they are the ones most JSON in
circulation was written with; an integer of up to 21 digits stays an
integer, and `1e300` is five characters rather than the 301 that Rust's
positional form alone would write. Every form is a JSON number. The
formatting writes into a scratch buffer the renderer keeps, so a
lexeme-less number costs no allocation.

## Codes raised here

`PROTOCOL_ORDER_ERROR`, `TARGET_VALUE_UNREPRESENTABLE`, `INVALID_NUMBER`,
`MISSING_VALUE`, `RESOURCE_LIMIT_EXCEEDED` (`max_output_bytes`),
`OUTPUT_FAILED`. A `Fail` from a renderer carries `committed_output` when
text the renderer wrote had reached the output's destination
(`TextOut::has_committed`): over a `WriteOut`, bytes the writer received,
not bytes still buffered, so the flag agrees with what `into_inner` hands
back. `RecordsToJson` sets it when it had forwarded any event, since the
stage downstream may have rendered them. A renderer's `is_done()` is true
only once `End` has been rendered AND flushed (or, for `RecordsToJson`,
forwarded and accepted); an `End` whose flush failed leaves the renderer
not done.

## Decisions where the design brief was silent

- `WriteOut` counts `output_bytes` when bytes reach the writer, not when
  they are accepted, so the metric means what its name says after a
  failure; `accepted()` is the other number.
- `into_inner` on `WriteOut` does not flush. The alternative, flushing on
  the way out, made `committed_output: false` untrue for every host that
  takes its file or standard output back after a failure: the bytes a
  failed document had buffered reached the writer after the failure had
  been reported as `output: "none"`. Dropping them is the only teardown
  that keeps the flag honest; `flush()` is explicit for anyone who wants
  the partial output.
- `Join`: a fragment outside an item is an item; unbalanced markers are
  protocol errors rather than panics.
- `ReplaceText`: an empty literal is the identity; `flush` is a boundary.
- `Quoting::Minimal` writes an empty field as nothing, so an empty string
  and an empty `null_text` read back the same; that is the dialect's
  trade-off, and `Always` is the standard profile for that reason.
- A non-finite value is refused even when a JSON-number lexeme stands
  beside it (`1e999`). The brief names NaN and infinity unrepresentable;
  writing the lexeme would produce JSON that serde_json, the crate's own
  oracle, rejects as out of range, and a CSV field whose number the
  pipeline could not carry. The CSV renderer refuses it too, so the two
  renderers agree on what a number is.
- Lexeme-less numbers use JavaScript's positional range, `[1e-6, 1e21)`,
  rather than Rust's positional-only form (301 digits for `1e300`) or the
  strictly shorter of the two layouts (which would write `1000` as `1e3`).
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
