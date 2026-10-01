/* Copyright (c) 2026 tabnas, MIT License */

// test/spec/json.tsv: `JsonEvents/1` events and `JsonOptions` in, the
// exact JSON text out, or the code of the first failure. The encodings are
// docs/reference.md's "Shared fixtures"; the harness is ./common.ts.

import { JsonOptions, JsonRenderer, StringOut } from '../dist/render'

import { Row, expectedText, feedJson, fields, jsonColumn, jsonEvents, malformed, runFixture } from './common'

function options(row: Row): Partial<JsonOptions> {
  const json = fields(row, jsonColumn(row, 'options', '{}'), ['indent', 'trailing_newline'])
  const out: Partial<JsonOptions> = {}
  if (undefined !== json.indent && null !== json.indent) {
    if (!Number.isInteger(json.indent) || (json.indent as number) < 0) {
      malformed(row, `indent ${JSON.stringify(json.indent)}`)
    }
    out.indent = json.indent as number
  }
  if (undefined !== json.trailing_newline) {
    if ('boolean' !== typeof json.trailing_newline) {
      malformed(row, `trailing_newline ${JSON.stringify(json.trailing_newline)}`)
    }
    out.trailingNewline = json.trailing_newline as boolean
  }
  return out
}

runFixture(
  'json.tsv',
  'events',
  (row) => {
    const events = jsonEvents(row, jsonColumn(row, 'events', ''))
    const renderer = new JsonRenderer(new StringOut(), options(row))
    feedJson(renderer, events)
    return renderer.intoInner().intoString()
  },
  { expected: expectedText },
)
