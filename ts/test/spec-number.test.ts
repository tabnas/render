/* Copyright (c) 2026 tabnas, MIT License */

// test/spec/number.tsv: one number (a lexeme, a value, or both) in, the
// text both renderers write for it out, or the code that refuses it.
//
// Each row runs twice, through the JSON renderer as a root scalar and
// through the CSV renderer as the one field of a minimally quoted,
// headerless, LF-terminated table, and the two must agree before the row
// is compared: a number is the one thing the two renderers share.

import { Cell, Ev, Fail } from '@tabnas/transduce'

import { CsvRenderer, JsonRenderer, StringOut } from '../dist/render'

import { Row, expectedText, jsonColumn, malformed, number, runFixture } from './common'

function throughJson(value: number, lexeme: string | null): string {
  const r = new JsonRenderer(new StringOut())
  r.event(Ev.number(value, lexeme))
  r.event(Ev.end)
  return r.intoInner().intoString()
}

function throughCsv(value: number, lexeme: string | null): string {
  const r = new CsvRenderer(new StringOut(), { header: false, newline: 'lf', quoting: 'minimal' })
  r.tableEvent({ type: 'schema', columns: [{ label: 'n' }] })
  r.tableEvent({ type: 'row', cells: [Cell.number(value, lexeme)] })
  r.tableEvent({ type: 'end' })
  const text = r.intoInner().intoString()
  return text.endsWith('\n') ? text.slice(0, -1) : text
}

type Outcome = { text: string } | { fail: Fail }

function outcome(run: () => string): Outcome {
  try {
    return { text: run() }
  } catch (err) {
    if (err instanceof Fail) return { fail: err }
    throw err
  }
}

function render(row: Row): string {
  const json = jsonColumn(row, 'number', '')
  if (null == json || 'object' !== typeof json || Array.isArray(json)) {
    return malformed(row, 'number is a number object')
  }
  const { value, lexeme } = number(row, json)
  const a = outcome(() => throughJson(value, lexeme))
  const b = outcome(() => throughCsv(value, lexeme))
  if ('text' in a && 'text' in b && a.text === b.text) return a.text
  if ('fail' in a && 'fail' in b && a.fail.code === b.fail.code) throw a.fail
  throw new Error(
    `${row.where()}: the renderers disagree: JSON ${JSON.stringify('text' in a ? a.text : a.fail.code)}, ` +
      `CSV ${JSON.stringify('text' in b ? b.text : b.fail.code)}`,
  )
}

runFixture('number.tsv', 'number', render, { expected: expectedText })
