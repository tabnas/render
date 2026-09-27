//! Renderers for the tabnas transducer protocols.
//!
//! A renderer decides how a semantic protocol becomes text: `TableRows/1`
//! becomes CSV, `JsonEvents/1` becomes JSON, incrementally, through a
//! coalescing writer with a byte budget. Source interpretation never
//! crosses into this crate: a renderer sees labels and cells, never paths.

#![forbid(unsafe_code)]

pub mod csv;
pub mod number;
pub mod text;

pub use csv::{CsvOptions, CsvRenderer, MissingText, Newline, Quoting};
pub use number::is_json_number;
pub use text::{Join, ReplaceText, StringOut, TextOut, WriteOut, DEFAULT_BUDGET};

/// This crate's version, as `Cargo.toml` declares it.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");
