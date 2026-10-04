//! Renderers for the tabnas transducer protocols.
//!
//! A renderer decides how a semantic protocol becomes text: `TableRows/1`
//! becomes CSV, `JsonEvents/1` becomes JSON, incrementally, through a
//! coalescing writer with a byte budget. Source interpretation never
//! crosses into this crate: a renderer sees labels and cells, never paths.
//!
//! - [`text`]: [`TextOut`], the fragment boundary every renderer writes
//!   to; [`WriteOut`], which coalesces fragments to a byte budget, enforces
//!   `max_output_bytes` and counts `output_bytes`; [`StringOut`] for tests
//!   and small results; [`Join`] and [`ReplaceText`], the two text
//!   combinators whose correctness depends on chunk boundaries, and
//!   [`Concat`], a `Join` with no separator under the name the language
//!   gives it.
//! - [`csv`]: [`CsvRenderer`], the always-quoted profile of `TableRows/1`,
//!   with [`CsvOptions`] for the dialects.
//! - [`json`]: [`JsonRenderer`], `JsonEvents/1` as compact or indented
//!   JSON text.
//! - [`records`]: [`RecordsToJson`], `TableRows/1` as `JsonEvents/1`, an
//!   array of objects keyed by label.
//! - [`number`]: the JSON number grammar both renderers hold lexemes to.
//! - [`renderers`](mod@renderers): [`RenderRenderers`], this crate's
//!   implementation of alchemy's `Renderers`, the interface a compiled
//!   alchemy program makes its rendering stages through.
//!
//! The protocols, the text boundary [`TextOut`] and the renderers' options
//! are tabnas-alchemy's shared types (`tabnas_alchemy::shared`),
//! re-exported here at the paths they have always had. Every renderer
//! validates its protocol as it goes and reports the stable codes of
//! [`tabnas_alchemy::shared::Code`]; the output is flushed once, at the
//! protocol's end, and a failure found after text was written says so with
//! `committed_output`.

#![forbid(unsafe_code)]

/// The README's Rust example runs as a doctest, so a stale one fails the
/// gate rather than misleading the reader.
#[cfg(doctest)]
#[doc = include_str!("../../README.md")]
mod readme_examples {}

pub mod csv;
pub mod json;
pub mod number;
pub mod records;
pub mod renderers;
pub mod text;

pub use csv::{CsvOptions, CsvRenderer, MissingText, Newline, Quoting};
pub use json::{JsonOptions, JsonRenderer};
pub use number::is_json_number;
pub use records::{MissingRecord, RecordsToJson};
pub use renderers::{renderers, RenderRenderers};
pub use text::{Concat, Join, ReplaceText, StringOut, TextOut, WriteOut, DEFAULT_BUDGET};

/// This crate's version, as `Cargo.toml` declares it.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");
