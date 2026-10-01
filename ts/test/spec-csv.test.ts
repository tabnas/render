/* Copyright (c) 2026 tabnas, MIT License */

// test/spec/csv.tsv: `TableRows/1` events and `CsvOptions` in, the exact
// CSV text out, or the code of the first failure. The encodings are
// docs/reference.md's "Shared fixtures"; the harness is ./common.ts.

import { CsvOptions, CsvRenderer, MissingText, StringOut } from '../dist/render'

import { Row, expectedText, feedTable, fields, jsonColumn, malformed, runFixture, tableEvents } from './common'

function options(row: Row): Partial<CsvOptions> {
  const json = fields(row, jsonColumn(row, 'options', '{}'), [
    'delimiter',
    'newline',
    'header',
    'null_text',
    'missing',
    'quoting',
  ])
  const out: Partial<CsvOptions> = {}
  if (undefined !== json.delimiter) {
    if ('string' !== typeof json.delimiter || 1 !== [...json.delimiter].length) {
      malformed(row, `delimiter ${JSON.stringify(json.delimiter)} is not one character`)
    }
    out.delimiter = json.delimiter as string
  }
  if (undefined !== json.newline) {
    if ('lf' !== json.newline && 'crlf' !== json.newline) malformed(row, `newline ${JSON.stringify(json.newline)}`)
    out.newline = json.newline as 'lf' | 'crlf'
  }
  if (undefined !== json.header) {
    if ('boolean' !== typeof json.header) malformed(row, `header ${JSON.stringify(json.header)}`)
    out.header = json.header as boolean
  }
  if (undefined !== json.null_text) {
    if ('string' !== typeof json.null_text) malformed(row, `null_text ${JSON.stringify(json.null_text)}`)
    out.nullText = json.null_text as string
  }
  if (undefined !== json.missing && null !== json.missing) {
    if ('string' !== typeof json.missing) malformed(row, `missing ${JSON.stringify(json.missing)}`)
    out.missing = MissingText.text(json.missing as string)
  }
  if (undefined !== json.quoting) {
    if ('always' !== json.quoting && 'minimal' !== json.quoting) malformed(row, `quoting ${JSON.stringify(json.quoting)}`)
    out.quoting = json.quoting as 'always' | 'minimal'
  }
  return out
}

runFixture(
  'csv.tsv',
  'events',
  (row) => {
    const events = tableEvents(row, jsonColumn(row, 'events', ''))
    const renderer = new CsvRenderer(new StringOut(), options(row))
    feedTable(renderer, events)
    return renderer.intoInner().intoString()
  },
  { expected: expectedText },
)
