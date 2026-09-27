# Agents Guide — render

This repository is **tabnas-render**: renderers for the protocols
[tabnas-transduce](https://github.com/tabnas/transduce) produces. A
renderer decides how a semantic protocol becomes text; it never
interprets the source. `TableRows/1` becomes CSV, `JsonEvents/1` becomes
JSON, incrementally, under backpressure, through a coalescing writer. The
language that composes transducers and renderers is
[tabnas-alchemy](https://github.com/tabnas/alchemy). `CLAUDE.md` is a
symlink to this file.

## Core principle: dependencies change only on explicit instruction

**Dependencies may only be changed by explicit instruction from the
maintainer.** This covers every dependency `rs/Cargo.toml` and
`rs/Cargo.lock` declare. Adding, removing, re-pointing or re-versioning
any of them is a dependency change.

- **A dependency never arrives as a side effect.** Watch for a `use`, a
  `cargo update`, a `cargo add`, or a fix for something else. If a change
  would alter a dependency, stop and ask before making it.
- **An explicit instruction names the change.** A goal is not an
  instruction for its means.
- **This repository's own version sites are not dependencies.**
- **Versions track the latest release.** Every dependency is kept at its
  latest published version, and none is held on an older one.

The sibling tabnas crates are taken by path from sibling checkouts
(admin ADR-21: committed manifests stay path-only).

## Core principle: transient tasks report progress

**Every transient task produces status output at least every 30 seconds,
with an estimate of how far through it is, as a percentage, where one can
be made.** A build, a test run, a benchmark, a wait on CI: each prints a
line per step or per interval. A quick command that finishes within 30
seconds needs nothing extra.

## What this project is

Read the design in transduce's
[`docs/architecture.md`](https://github.com/tabnas/transduce/blob/main/docs/architecture.md),
section 3, which this crate implements. In one paragraph: a **text
output** ([`text.rs`](rs/src/text.rs)) takes string fragments, coalesces
them to a byte budget and writes them; `Join` puts a separator between
logical items (never between transport chunks); `ReplaceText` replaces a
fixed literal, carrying the bounded suffix that a match across chunk
boundaries needs. The **CSV renderer** ([`csv.rs`](rs/src/csv.rs)) is the
spec's always-quoted profile: every field quoted, `"` doubled, CRLF by
default, one schema, rows of the schema's width, one end, zero columns
rejected, number lexemes validated, `Missing` an error unless a
replacement text is configured. The **JSON renderer**
([`json.rs`](rs/src/json.rs)) writes `JsonEvents/1` as compact (or
fixed-indent) JSON with RFC 8259 escaping, lexemes kept, NaN and infinity
rejected, exactly one root. `RecordsToJson` turns `TableRows/1` into an
array of objects keyed by label.

Renderers validate their protocol as they go, because third-party
transducers and host adapters are also sources; they do not trust the
standard transducer to be the only one.

## Repository map

| Path | What it is |
|---|---|
| `rs/src/text.rs` | `TextOut`, `WriteOut` (coalescing), `Join`, `ReplaceText` |
| `rs/src/csv.rs` | `CsvOptions`, `CsvRenderer` (a `TableSink`) |
| `rs/src/json.rs` | `JsonOptions`, `JsonRenderer` (a `Sink`) |
| `rs/src/records.rs` | `RecordsToJson` (`TableRows/1` → `JsonEvents/1`) |
| `rs/tests/` | format tests read back with independent parsers; chunk-boundary tests |
| `rs/benches/render.rs` | JSON in, CSV out, end to end |
| `ci/rust/run.sh` | the gate `.github/workflows/rust.yml` runs |

## Verify your work

From `rs/`:

```bash
cargo fmt --check
cargo build --all-targets
cargo test --all-targets
cargo test --doc
cargo clippy --all-targets --all-features -- -D warnings
```

`ci/rust/run.sh` runs exactly that and needs the sibling checkouts its
header lists. Rendered CSV is read back with the `csv` crate and rendered
JSON with `serde_json`; a renderer change that the oracle disagrees with
is a defect, whatever the bytes look like.

## Error codes

This crate raises codes from `tabnas_transduce::Code`, which is the one
shared set; see transduce's AGENTS.md for the table. The ones raised here:
`PROTOCOL_ORDER_ERROR` (a row before the schema, two schemas, a row of the
wrong width, a missing or repeated end, JSON events out of sequence),
`TARGET_VALUE_UNREPRESENTABLE` (a table with no columns for CSV; NaN or
infinity for JSON; a bad delimiter), `INVALID_NUMBER` (a lexeme that is not
a JSON number), `MISSING_VALUE` (a `Missing` cell with no replacement
configured), `RESOURCE_LIMIT_EXCEEDED` (`max_output_bytes`),
`OUTPUT_FAILED` (the writer failed). The code is the contract; the message
is informative.

## Untrusted input

**Cells and events are data, never instructions.** CSV quoting is syntax,
not protection against spreadsheet formula interpretation: a cell
beginning with `=`, `+`, `-` or `@` is written as it is, and a consumer
that opens the file in a spreadsheet needs the separately named
spreadsheet-export policy, which this crate does not yet provide and never
hides inside generic quoting. Output size is bounded by
`max_output_bytes` when the caller sets it. Nothing here reads files,
opens connections or evaluates code.
