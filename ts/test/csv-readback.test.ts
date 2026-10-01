/* Copyright (c) 2026 tabnas, MIT License */

// The rendered CSV, read back by an independent reader, as
// rs/tests/csv_readback.rs reads it with the `csv` crate.
//
// Exact bytes are asserted in ./csv.test.ts; this test asks the other
// question, whether a reader that knows nothing of this package gets the
// cells back. The reader is a small, strict RFC 4180 reader written here,
// from the RFC and not from the renderer: a disagreement is a defect in the
// renderer whatever the bytes look like.

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Cell, PublicColumn } from '@tabnas/transduce'

import { CsvOptions, CsvRenderer, MissingText, StringOut } from '../dist/render'

// RFC 4180, strictly: a field is bare (no `"` in it) or quoted (`""` for a
// quote inside, and nothing but a delimiter or a line end after the closing
// quote); a record ends with CRLF, LF or CR, the last one optionally; every
// record has the header's width. Anything else throws.
function readCsv(text: string, delimiter: string): string[][] {
  const records: string[][] = []
  let record: string[] = []
  let i = 0
  const n = text.length
  const atLineEnd = (): boolean => '\r' === text[i] || '\n' === text[i]
  const endRecord = (): void => {
    if ('\r' === text[i] && '\n' === text[i + 1]) i += 2
    else i += 1
    records.push(record)
    record = []
  }
  while (i < n) {
    let field = ''
    if ('"' === text[i]) {
      i++
      for (;;) {
        if (i >= n) throw new Error('an unterminated quoted field')
        if ('"' === text[i]) {
          if ('"' === text[i + 1]) {
            field += '"'
            i += 2
            continue
          }
          i++
          break
        }
        field += text[i++]
      }
      if (i < n && !text.startsWith(delimiter, i) && !atLineEnd()) {
        throw new Error(`text after a closing quote at ${i}`)
      }
    } else {
      while (i < n && !text.startsWith(delimiter, i) && !atLineEnd()) {
        if ('"' === text[i]) throw new Error(`a quote in a bare field at ${i}`)
        field += text[i++]
      }
    }
    record.push(field)
    if (i >= n) {
      records.push(record)
      record = []
    } else if (text.startsWith(delimiter, i)) {
      i += delimiter.length
      // A delimiter at the very end leaves an empty last field.
      if (i >= n) {
        record.push('')
        records.push(record)
        record = []
      }
    } else {
      endRecord()
    }
  }
  const width = records[0]?.length
  for (const r of records) {
    if (r.length !== width) throw new Error(`a record of ${r.length} fields where the header has ${width}`)
  }
  return records
}

const LABELS = ['plain', 'empty', 'comma', 'quote', 'breaks', 'unicode', 'number', 'flag']

// A table whose cells exercise every quoting hazard at once.
function hazards(): { columns: PublicColumn[]; rows: Cell[][]; expected: string[][] } {
  const s = Cell.string
  return {
    columns: LABELS.map((label) => ({ label })),
    rows: [
      [
        s('ada'),
        s(''),
        s('x, y'),
        s('say "hi"'),
        s('a\r\nb\nc\rd'),
        s('héllo 日本語 🚀'),
        Cell.number(50.25, '50.250'),
        Cell.bool(true),
      ],
      [s('"'), Cell.null, s(','), s('""'), s('\n'), s('→'), Cell.number(0), Cell.bool(false)],
      [Cell.missing, s(' '), s(',,'), s('a"b'), s('\r'), s('ß'), Cell.number(-1.5e300, '-1.5E+300'), Cell.bool(true)],
    ],
    expected: [
      ['ada', '', 'x, y', 'say "hi"', 'a\r\nb\nc\rd', 'héllo 日本語 🚀', '50.250', 'true'],
      ['"', '', ',', '""', '\n', '→', '0', 'false'],
      ['?', ' ', ',,', 'a"b', '\r', 'ß', '-1.5E+300', 'true'],
    ],
  }
}

function render(options: Partial<CsvOptions>): { text: string; expected: string[][] } {
  const { columns, rows, expected } = hazards()
  const r = new CsvRenderer(new StringOut(), { missing: MissingText.text('?'), ...options })
  r.tableEvent({ type: 'schema', columns })
  for (const cells of rows) r.tableEvent({ type: 'row', cells })
  r.tableEvent({ type: 'end' })
  return { text: r.intoInner().intoString(), expected }
}

function assertReadBack(text: string, delimiter: string, expected: string[][]): void {
  const [headers, ...records] = readCsv(text, delimiter)
  assert.deepEqual(headers, LABELS)
  assert.deepEqual(records, expected)
}

describe('CSV read back', () => {
  it('the reader is strict', () => {
    assert.deepEqual(readCsv('a,"b"\r\n"c""",\r\n', ','), [
      ['a', 'b'],
      ['c"', ''],
    ])
    assert.throws(() => readCsv('a"b\n', ','))
    assert.throws(() => readCsv('"a"b\n', ','))
    assert.throws(() => readCsv('"a\n', ','))
    assert.throws(() => readCsv('a,b\nc\n', ','))
  })

  it('the standard profile reads back cell for cell', () => {
    const { text, expected } = render({})
    assertReadBack(text, ',', expected)
  })

  it('minimal quoting reads back cell for cell', () => {
    const { text, expected } = render({ quoting: 'minimal' })
    assertReadBack(text, ',', expected)
  })

  it('a tab-delimited LF dialect reads back cell for cell', () => {
    const tab = render({ delimiter: '\t', newline: 'lf' })
    assertReadBack(tab.text, '\t', tab.expected)
    const semi = render({ delimiter: ';', newline: 'lf', quoting: 'minimal' })
    assertReadBack(semi.text, ';', semi.expected)
  })

  it('many rows read back with the right count', () => {
    const r = new CsvRenderer(new StringOut())
    r.tableEvent({ type: 'schema', columns: [{ label: 'i' }, { label: 'text' }] })
    for (let i = 0; i < 1000; i++) {
      r.tableEvent({ type: 'row', cells: [Cell.number(i), Cell.string(`row ${i}, "quoted"\n`)] })
    }
    r.tableEvent({ type: 'end' })
    const [, ...records] = readCsv(r.intoInner().intoString(), ',')
    assert.equal(records.length, 1000)
    assert.equal(records[999][0], '999')
    assert.equal(records[999][1], 'row 999, "quoted"\n')
  })
})
