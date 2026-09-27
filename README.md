# tabnas-render

Renderers for the [tabnas-transduce](https://github.com/tabnas/transduce)
protocols: streamed tables become CSV, streamed events become JSON,
incrementally and under backpressure. A renderer decides how a semantic
protocol becomes text and never interprets the source; that separation is
the point.

```
TableRows/1 ──▶ CsvRenderer ──▶ text     JsonEvents/1 ──▶ JsonRenderer ──▶ text
```

- **Always-quoted CSV** (the standard profile): every field quoted, `"`
  doubled, CRLF line endings, exact number lexemes, explicit null and
  missing policies. Other delimiters are an explicitly selected dialect.
- **Compact JSON** with RFC 8259 escaping and lexemes kept; fixed-indent
  pretty printing as a separate profile.
- **Validated as it goes**: one schema, rows of the schema's width, one
  end; protocol errors carry the stable codes every stage shares.

Rust only for now. The language that composes transducers and renderers
is [tabnas-alchemy](https://github.com/tabnas/alchemy);
[aless](https://github.com/rjrodger/aless) exposes both from the command
line.

## Layout

| Path | What it is |
|---|---|
| [`rs/`](rs/) | the `tabnas-render` crate (library `tabnas_render`) |
| [`docs/`](docs/) | reference notes; the design lives in transduce's `docs/architecture.md` |
| [`ci/rust/run.sh`](ci/rust/run.sh) | the gate CI runs |

## Build and test

Sibling checkouts (`../transduce`, `../parser`, the grammars) are named by
path in `rs/Cargo.toml`.

```bash
make build
make test
```

Contributors and agents: read [`AGENTS.md`](AGENTS.md).

## License

MIT. Copyright (c) Richard Rodger.
