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
// codes of `@tabnas/transduce`'s `Code`; the output is flushed once, at
// the protocol's end, and a failure found after text was written says so
// with `committedOutput`. Everything is synchronous: a renderer is a sink
// the transducer calls, and a slow writer slows the parse.

// This package's version, as package.json declares it.
export const VERSION = '0.1.0'

export { CsvOptions, CsvRenderer, MissingText, Newline } from './csv'
export type { Quoting } from './csv'
export { JsonOptions, JsonRenderer } from './json'
export { checkNumber, isJsonNumber, writeValue } from './number'
export { RecordsToJson } from './records'
export type { MissingRecord } from './records'
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
export type { TextOut, Writer } from './text'
