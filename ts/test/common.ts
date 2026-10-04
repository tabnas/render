/* Copyright (c) 2026 tabnas, MIT License */

// The harness behind the shared fixtures in ../../test/spec, which every
// runtime of this package runs.
//
// What a row means is documented in docs/reference.md ("Shared fixtures"):
// what each column decodes to and how the result is compared. This harness
// reproduces exactly that, as rs/tests/common/mod.rs does for Rust; nothing
// here is specific to TypeScript except the calls into this package's API.
// A fixture cell that does not follow the encodings is a defect in the
// fixture: it throws a plain `Error` naming the row, which no `ERROR:<CODE>`
// row can match, so it fails loudly rather than passing by accident.

import { join } from 'node:path'
import { describe, it } from 'node:test'
import assert from 'node:assert'

import { findSpecDir, loadSpec, makeRunner, unescape } from '@tabnas/support'
import {
  Cell,
  Ev,
  Fail,
  JsonEvent,
  PublicColumn,
  Sink,
  TableEvent,
  TableSink,
} from '@tabnas/alchemy/shared'

import { isJsonNumber } from '../dist/render'

// A fixture row, as @tabnas/support's loader hands it over.
export type Row = {
  line: number
  named(name: string): string
  unescNamed(name: string): string
  where(): string
}

// The shared `test/spec` directory, found by walking up from here.
export function specDir(): string {
  return findSpecDir(__dirname)
}

// The fixture files, each with its runner: a new file without a runner
// fails `every fixture has a runner` rather than passing unread.
export const FIXTURES = ['csv.tsv', 'json.tsv', 'number.tsv', 'records.tsv', 'text.tsv']

// A malformed fixture cell: loud, with the row it came from.
export function malformed(row: Row, what: string): never {
  throw new Error(`${row.where()}: malformed fixture cell: ${what}`)
}

// A JSON column, read RAW: the cell is not passed through the escape codec,
// because JSON has escapes of its own (`\n` inside a JSON string is two
// characters, and the codec would turn it into a line feed that JSON does
// not allow). An empty cell is `fallback`.
export function jsonColumn(row: Row, name: string, fallback: string): any {
  const cell = row.named(name)
  const text = '' === cell ? fallback : cell
  try {
    return JSON.parse(text)
  } catch (err) {
    return malformed(row, `${name}: ${err}`)
  }
}

function isObject(json: unknown): json is Record<string, unknown> {
  return null != json && 'object' === typeof json && !Array.isArray(json)
}

// An options object's field, or `undefined` when absent; every field must
// be one of `known`, since a misspelt option would otherwise be ignored and
// the row would test the default.
export function fields(row: Row, options: unknown, known: string[]): Record<string, unknown> {
  if (!isObject(options)) return malformed(row, `options ${JSON.stringify(options)} is not an object`)
  for (const k of Object.keys(options)) {
    if (!known.includes(k)) malformed(row, `unknown option ${JSON.stringify(k)}`)
  }
  return options
}

// A value spelled as fixture text: a JSON number that is finite as a
// double, or `NaN`, `Infinity` or `-Infinity`. Every runtime's decimal
// parser rounds to the nearest double, so the spelling names one value
// everywhere; an overflowing spelling is refused, so infinity is always
// written as such.
export function parseValue(row: Row, text: string): number {
  if ('NaN' === text) return NaN
  if ('Infinity' === text) return Infinity
  if ('-Infinity' === text) return -Infinity
  if (isJsonNumber(text)) {
    const v = Number(text)
    if (Number.isFinite(v)) return v
  }
  return malformed(row, `${JSON.stringify(text)} is not a value`)
}

// A number object: `{"num": "<lexeme>"}`, `{"value": "<value>"}`, or both.
// With a lexeme and no value, the value is the lexeme read as a decimal
// when it is a JSON number (overflowing to an infinity, as `1e999` does in
// every runtime) and 0 when it is not; the renderers judge the lexeme
// before the value, so that 0 never decides a row.
export function number(row: Row, object: Record<string, unknown>): { value: number; lexeme: string | null } {
  for (const k of Object.keys(object)) {
    if ('num' !== k && 'value' !== k) malformed(row, `unknown number field ${JSON.stringify(k)}`)
  }
  const text = (key: string): string | undefined => {
    const v = object[key]
    if (undefined === v) return undefined
    if ('string' !== typeof v) return malformed(row, `${key} must be a string, not ${JSON.stringify(v)}`)
    return v
  }
  const lexeme = text('num')
  const valueText = text('value')
  let value: number
  if (undefined !== valueText) value = parseValue(row, valueText)
  else if (undefined !== lexeme) value = isJsonNumber(lexeme) ? Number(lexeme) : 0
  else return malformed(row, 'a number object names num, value or both')
  return { value, lexeme: lexeme ?? null }
}

// One `TableRows/1` cell: `null`, `true`, `false`, a JSON string, a number
// object, or `{"missing": true}`. A bare JSON number is refused: it cannot
// say whether it carries a lexeme.
export function cell(row: Row, json: unknown): Cell {
  if (null === json) return Cell.null
  if ('boolean' === typeof json) return Cell.bool(json)
  if ('string' === typeof json) return Cell.string(json)
  if (isObject(json)) {
    const keys = Object.keys(json)
    if (1 === keys.length && true === json.missing) return Cell.missing
    const { value, lexeme } = number(row, json)
    return Cell.number(value, lexeme)
  }
  return malformed(row, `${JSON.stringify(json)} is not a cell`)
}

// The `events` column of a table fixture: `{"schema": [labels]}`,
// `{"row": [cells]}` and `"end"`, in order.
export function tableEvents(row: Row, json: unknown): TableEvent[] {
  if (!Array.isArray(json)) return malformed(row, 'events is a JSON array')
  return json.map((item): TableEvent => {
    if ('end' === item) return { type: 'end' }
    if (isObject(item) && 1 === Object.keys(item).length) {
      if (Array.isArray(item.schema)) {
        const columns: PublicColumn[] = item.schema.map((l: unknown) =>
          'string' === typeof l ? { label: l } : malformed(row, `label ${JSON.stringify(l)} is not a string`),
        )
        return { type: 'schema', columns }
      }
      if (Array.isArray(item.row)) {
        return { type: 'row', cells: item.row.map((c: unknown) => cell(row, c)) }
      }
    }
    return malformed(row, `${JSON.stringify(item)} is not a table event`)
  })
}

// Send table events to a sink in order; the first failure is the result.
export function feedTable(sink: TableSink, events: TableEvent[]): void {
  for (const ev of events) sink.tableEvent(ev)
}

// The `events` column of a JSON fixture: `"{"`, `"}"`, `"["`, `"]"` and
// `"end"` for the structural events; `{"key": k}` and `{"str": s}`;
// `null`, `true`, `false`; a number object.
export function jsonEvents(row: Row, json: unknown): JsonEvent[] {
  if (!Array.isArray(json)) return malformed(row, 'events is a JSON array')
  return json.map((item): JsonEvent => {
    switch (item) {
      case '{':
        return Ev.objectStart
      case '}':
        return Ev.objectEnd
      case '[':
        return Ev.arrayStart
      case ']':
        return Ev.arrayEnd
      case 'end':
        return Ev.end
      case null:
        return Ev.null
      case true:
      case false:
        return Ev.bool(item)
    }
    if (isObject(item)) {
      const keys = Object.keys(item)
      if (1 === keys.length && 'key' === keys[0]) {
        if ('string' !== typeof item.key) return malformed(row, `key ${JSON.stringify(item.key)} is not a string`)
        return Ev.key(item.key)
      }
      if (1 === keys.length && 'str' === keys[0]) {
        if ('string' !== typeof item.str) return malformed(row, `str ${JSON.stringify(item.str)} is not a string`)
        return Ev.string(item.str)
      }
      const { value, lexeme } = number(row, item)
      return Ev.number(value, lexeme)
    }
    return malformed(row, `${JSON.stringify(item)} is not a JSON event`)
  })
}

// Send JSON events to a sink in order; the first failure is the result.
export function feedJson(sink: Sink, events: JsonEvent[]): void {
  for (const ev of events) sink.event(ev)
}

// The expected column of a text-producing fixture: the exact output,
// written through the escape codec (`\r`, `\n`, `\t` and `\\` decoded,
// everything else as it stands). Reached only for a value row; the runner
// reads `ERROR:<CODE>` itself.
export function expectedText(cell: string): string {
  return unescape(cell)
}

// A row this runtime cannot pass for a reason of the runtime's, recorded in
// DIVERGENCE.md: the row's input cell, RAW as the file holds it, and why. A skipped row is
// reported as skipped, by this reason, never dropped silently; a skip that
// matches no row fails the run.
export type Skip = { input: string; why: string }

// Run one fixture file through @tabnas/support's runner, every row through
// `stage` except the `skips`. The `input` column names the case; `stage`
// reads its cells RAW from the row, since the runner's input is the
// escape-decoded cell and these inputs are JSON.
export function runFixture(
  file: string,
  input: string,
  stage: (row: Row) => unknown,
  options: { expected?: (cell: string) => unknown; skips?: Skip[] } = {},
): void {
  const skips = options.skips ?? []
  const spec = loadSpec(join(specDir(), file))
  const used = new Set<Skip>()
  spec.rows = spec.rows.filter((row: any) => {
    const skip = skips.find((s) => s.input === row.named(input))
    if (undefined === skip) return true
    used.add(skip)
    describe('spec: ' + file + ' (runtime divergence)', () => {
      it(`row ${row.line}: ${row.named(input)}`, { skip: skip.why }, () => {})
    })
    return false
  })
  describe('spec: ' + file + ' skips', () => {
    it('every skip names a row', () => {
      assert.deepEqual(
        skips.filter((s) => !used.has(s)),
        [],
        'a skip that matches no row is stale; remove it',
      )
    })
  })
  const parseExpected = options.expected
  makeRunner({
    input,
    expected: 'expected',
    parse: (_text: string, row: any) => stage(row),
    errorCode: (err: unknown) => (err instanceof Fail ? err.code : undefined),
    ...(parseExpected ? { parseExpected: (cell: string) => parseExpected(cell) } : {}),
  }).spec(spec)
}
