/* Copyright (c) 2026 tabnas, MIT License */

// test/spec/records.tsv: `TableRows/1` events and a missing policy in,
// through `RecordsToJson` and a compact `JsonRenderer`, the exact JSON text
// out, or the code of the first failure. The harness is ./common.ts.

import { JsonRenderer, MissingRecord, RecordsToJson, StringOut } from '../dist/render'

import { Row, expectedText, feedTable, fields, jsonColumn, malformed, runFixture, tableEvents } from './common'

function missing(row: Row): MissingRecord {
  const json = fields(row, jsonColumn(row, 'options', '{}'), ['missing'])
  const m = json.missing
  if (undefined === m) return 'skip'
  if ('skip' === m || 'null' === m || 'error' === m) return m
  return malformed(row, `missing ${JSON.stringify(m)}`)
}

runFixture(
  'records.tsv',
  'events',
  (row) => {
    const events = tableEvents(row, jsonColumn(row, 'events', ''))
    const renderer = new JsonRenderer(new StringOut())
    const records = new RecordsToJson(renderer).withMissing(missing(row))
    feedTable(records, events)
    return records.intoInner().intoInner().intoString()
  },
  { expected: expectedText },
)
