// Copyright (c) 2026 tabnas, MIT License

// Package tabnasrender is the Go port of tabnas-render: renderers for the
// protocols tabnas-transduce produces.
//
// A renderer decides how a semantic protocol becomes text: TableRows/1
// becomes CSV, JsonEvents/1 becomes JSON, incrementally, through a
// coalescing writer with a byte budget. Source interpretation never
// crosses into this package: a renderer sees labels and cells, never
// paths. It consumes the transduce Go port's types
// (github.com/tabnas/transduce/go) exactly as the Rust crate consumes
// tabnas_transduce's.
//
//   - [TextOut], the fragment boundary every renderer writes to;
//     [WriteOut], which coalesces fragments to a byte budget, enforces
//     max_output_bytes and counts OutputBytes; [StringOut] for tests and
//     small results; [Join] and [ReplaceText], the two text combinators
//     whose correctness depends on chunk boundaries, and [Concat], a Join
//     with no separator.
//   - [CSVRenderer], the always-quoted profile of TableRows/1, with
//     [CSVOptions] for the dialects.
//   - [JSONRenderer], JsonEvents/1 as compact or indented JSON text.
//   - [RecordsToJSON], TableRows/1 as JsonEvents/1, an array of objects
//     keyed by label.
//   - [IsJSONNumber], the JSON number grammar both renderers hold lexemes
//     to, and [FormatValue], the text of a number without one.
//
// Every renderer validates its protocol as it goes and reports the stable
// codes of transduce's Code; the output is flushed once, at the
// protocol's end, and a failure found after text was written says so with
// CommittedOutput.
//
// The Rust crate in ../rs is the reference; ../docs/reference.md
// describes the contracts, and the shared fixtures in ../test/spec pin
// them for every runtime. ../DIVERGENCE.md records where this port
// differs.
package tabnasrender

// VERSION is this module's version. It must equal rs/Cargo.toml's
// [package] version; version_test.go fails the build when they drift.
const VERSION = "0.1.0"
