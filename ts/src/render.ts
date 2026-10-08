/* Copyright (c) 2026 tabnas, MIT License */

// Renderers for the tabnas transducer protocols.
//
// A renderer decides how a semantic protocol becomes text: `TableRows/1`
// becomes CSV, `JsonEvents/1` becomes JSON, incrementally, through a
// coalescing writer with a byte budget. Source interpretation never
// crosses into this package: a renderer sees labels and cells, never
// paths.
//
// - text: `TextOut`, the fragment boundary every renderer writes to;
//   `WriteOut`, which coalesces fragments to a byte budget, enforces
//   `max_output_bytes` and counts `output_bytes`; `StringOut` for tests
//   and small results; `Join` and `ReplaceText`, the two text combinators
//   whose correctness depends on chunk boundaries, and `Concat`, a `Join`
//   with no separator under the name the language gives it.
// - csv: `CsvRenderer`, the always-quoted profile of `TableRows/1`, with
//   `CsvOptions` for the dialects.
// - json: `JsonRenderer`, `JsonEvents/1` as compact or indented JSON text.
// - records: `RecordsToJson`, `TableRows/1` as `JsonEvents/1`, an array of
//   objects keyed by label.
// - number: the JSON number grammar both renderers hold lexemes to.
//
// Every renderer validates its protocol as it goes and reports the stable
// codes of the shared `Code`; the output is flushed once, at the
// protocol's end, and a failure found after text was written says so with
// `committedOutput`. Everything is synchronous: a renderer is a sink the
// transducer calls, and a slow writer slows the parse.
//
// The protocols this package renders, `Fail` and its codes, the text
// boundary (`TextOut`, `Writer`) and the renderers' options (`CsvOptions`,
// `MissingText`, `Newline`, `Quoting`, `JsonOptions`, `MissingRecord`) are
// alchemy's shared types (`@tabnas/alchemy/shared`), which this package
// imports, and re-exports where it always exported them. `renderers` is
// this package's renderers and text stages as alchemy's `Renderers`, for a
// host to pass to alchemy's `compile`.

// This package's version, as package.json declares it.
export const VERSION = '0.2.3'

export { CsvRenderer } from './csv'
export { CsvOptions, MissingText, Newline } from '@tabnas/alchemy/shared'
export type { Quoting } from '@tabnas/alchemy/shared'
export { JsonRenderer } from './json'
export { JsonOptions } from '@tabnas/alchemy/shared'
export { checkNumber, isJsonNumber, writeValue } from './number'
export { RecordsToJson } from './records'
export type { MissingRecord } from '@tabnas/alchemy/shared'
export {
  BytesWriter,
  Concat,
  DEFAULT_BUDGET,
  FdWriter,
  Join,
  ReplaceText,
  StringOut,
  WriteOut,
  hasCommitted,
} from './text'
export type { TextOut, Writer } from '@tabnas/alchemy/shared'
export { renderers } from './renderers'
