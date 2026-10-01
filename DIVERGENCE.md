# Divergences

Where a port of tabnas-render produces a different result from the
others for the same input, and why.

The Rust crate in [`rs/`](rs/) is the only runtime today; TypeScript
(`ts/`) and Go (`go/`) ports are approved and not yet started. Every
runtime runs the same rows in [`test/spec/`](test/spec/), whose encodings
[`docs/reference.md`](docs/reference.md) ("Shared fixtures") defines. A
divergence recorded here is not a licence to keep it: each entry names
what differs, where the repair belongs, and the test that fails the day
it lands.

## None are recorded today

There is one runtime, so there is nothing for it to disagree with. When a
port lands, every shared row runs in it with no exemption; a row it
cannot satisfy is recorded here, with its reason and its owner, in the
same change that adds the port.

The number rows are where a port is most likely to differ, because the
fixture's layout of a lexeme-less number is no runtime's default
(`1e21`, not JavaScript's `1e+21` or Go's `1e+21`; `1e-7`, not Go's
`1e-07`; `-0`, not JavaScript's `0`). That is a port normalising its
formatter to [`test/spec/number.tsv`](test/spec/number.tsv), not a
divergence to record.

## What stays out of the shared files

These are Rust tests that are not, and are not meant to become, shared
rows. They are not divergences: a port carries its own equivalent where
the question applies to it, and none of them asks anything a row could
answer in every runtime.

- **Read-back oracles.** [`rs/tests/csv_readback.rs`](rs/tests/csv_readback.rs)
  reads rendered CSV back with the `csv` crate, and
  [`rs/tests/json_readback.rs`](rs/tests/json_readback.rs) reads rendered
  JSON back with `serde_json`, over the documents in `rs/tests/fixtures/`
  parsed by the Rust grammars. The oracle is a Rust library; a port uses
  an independent reader of its own language for the same question.
- **Benchmarks.** [`rs/benches/render.rs`](rs/benches/render.rs)
  (criterion) measures throughput. Numbers are per runtime and per
  machine, and are recorded in `docs/reference.md`, not pinned.
- **What a row cannot record.** A row pins the output or the first
  failure's code. `committed_output`, the text a failed run leaves behind
  (a rejected row or number writes nothing), a writer that fails or takes
  part of a buffer (`OUTPUT_FAILED`, the short-write accounting),
  `Flow::Stop` from a sink, the `output_bytes` metric, `is_done` after a
  failed flush, and streaming without copies (escaped strings written run
  by run, keys borrowed from the labels) are asserted beside the code in
  `rs/src/*.rs`.
