# tabnas-render (Rust)

The `tabnas-render` crate, library `tabnas_render`: incremental CSV and
JSON renderers for the `tabnas-transduce` protocols. See the repository
[README](../README.md) and [AGENTS.md](../AGENTS.md); the design is
transduce's `docs/architecture.md`, section 3.

The transducer crate, the engine and the test grammars are sibling
checkouts named by path in `Cargo.toml`. From this directory:
`cargo test --all-targets`, `cargo test --doc`,
`cargo clippy --all-targets --all-features -- -D warnings`.
