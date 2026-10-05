# tabnas-render (Rust)

The `tabnas-render` crate, library `tabnas_render`: incremental CSV and
JSON renderers for the `tabnas-transduce` protocols. See the repository
[README](../README.md) and [AGENTS.md](../AGENTS.md); the design is
transduce's `docs/architecture.md`, section 3.

The protocols, the text boundary `TextOut` and the renderers' options are
tabnas-alchemy's shared types, `tabnas_alchemy::shared`, re-exported here
at the paths they have always had; `renderers()` answers this crate's
implementation of alchemy's `Renderers`, which a host passes to
`tabnas_alchemy::compile`.

alchemy's shared types, the engine, the transducer crate the tests and
benches feed the renderers from, and the test grammars are sibling
checkouts named by path in `Cargo.toml`. From this directory:
`cargo test --all-targets`, `cargo test --doc`,
`cargo clippy --all-targets --all-features -- -D warnings`.
