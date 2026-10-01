/* Copyright (c) 2026 tabnas, MIT License */

// RecordsToJson beside the fixtures, ported from rs/src/records.rs: the
// events it forwards, repeated labels, the protocol, a sink that stops or
// fails, and committed output.

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Cell, Ev, EventRecorder, Fail, FnSink, JsonEvent, PublicColumn, TableEvent } from '@tabnas/transduce'

import { JsonRenderer, MissingRecord, RecordsToJson, StringOut } from '../dist/render'

function cols(labels: string[]): PublicColumn[] {
  return labels.map((label) => ({ label }))
}

const s = Cell.string
const schema = (labels: string[]): TableEvent => ({ type: 'schema', columns: cols(labels) })
const row = (cells: Cell[]): TableEvent => ({ type: 'row', cells })
const END: TableEvent = { type: 'end' }
const { objectStart: OS, objectEnd: OE, arrayStart: AS, arrayEnd: AE } = Ev
const key = Ev.key
const str = Ev.string

function run(missing: MissingRecord, labels: string[], rows: Cell[][]): JsonEvent[] {
  const r = new RecordsToJson(new EventRecorder()).withMissing(missing)
  r.tableEvent(schema(labels))
  for (const cells of rows) r.tableEvent(row(cells))
  r.tableEvent(END)
  assert.equal(r.isDone(), true)
  return r.intoInner().events
}

function fails(code: string, fn: () => unknown): Fail {
  try {
    fn()
  } catch (err) {
    assert.ok(err instanceof Fail, `not a Fail: ${err}`)
    assert.equal(err.code, code, err.message)
    return err
  }
  assert.fail(`expected ${code}`)
}

describe('RecordsToJson', () => {
  it('a table becomes an array of objects keyed by label', () => {
    const rows = [
      [Cell.number(1, '1.0'), s('ada'), Cell.bool(true)],
      [Cell.number(2), Cell.null, Cell.bool(false)],
    ]
    assert.deepEqual(run('skip', ['id', 'name', 'ok'], rows), [
      AS,
      OS,
      key('id'),
      Ev.number(1, '1.0'),
      key('name'),
      str('ada'),
      key('ok'),
      Ev.bool(true),
      OE,
      OS,
      key('id'),
      Ev.number(2),
      key('name'),
      Ev.null,
      key('ok'),
      Ev.bool(false),
      OE,
      AE,
      Ev.end,
    ])
  })

  it('no rows is an empty array', () => {
    assert.deepEqual(run('skip', ['a'], []), [AS, AE, Ev.end])
  })

  it('zero columns gives empty objects', () => {
    assert.deepEqual(run('skip', [], [[], []]), [AS, OS, OE, OS, OE, AE, Ev.end])
  })

  it('a missing cell is skipped by default', () => {
    const r = new RecordsToJson(new EventRecorder())
    r.tableEvent(schema(['a', 'b']))
    r.tableEvent(row([Cell.missing, s('x')]))
    r.tableEvent(END)
    assert.deepEqual(r.intoInner().events, [AS, OS, key('b'), str('x'), OE, AE, Ev.end])
  })

  it('a missing cell can be null or an error', () => {
    assert.deepEqual(run('null', ['a', 'b'], [[Cell.missing, s('x')]]), [
      AS,
      OS,
      key('a'),
      Ev.null,
      key('b'),
      str('x'),
      OE,
      AE,
      Ev.end,
    ])
    const r = new RecordsToJson(new EventRecorder(), 'error')
    r.tableEvent(schema(['a', 'b']))
    const err = fails('MISSING_VALUE', () => r.tableEvent(row([s('x'), Cell.missing])))
    assert.match(err.message, /"b"/)
    assert.equal(err.committedOutput, true, 'the array start was forwarded')
    assert.deepEqual(r.intoInner().events, [AS], 'nothing of the row was')
  })

  it('a repeated label keeps the last value that is present', () => {
    // Row two's last "a" is missing and skipped, so the reader of the
    // un-deduplicated record would keep "4"; row three has no "a" at all.
    // Under `null` the missing member is there, and wins.
    const rows = [
      [s('1'), s('2'), s('3')],
      [s('4'), s('5'), Cell.missing],
      [Cell.missing, s('6'), Cell.missing],
    ]
    assert.deepEqual(run('skip', ['a', 'b', 'a'], rows), [
      AS,
      OS,
      key('b'),
      str('2'),
      key('a'),
      str('3'),
      OE,
      OS,
      key('a'),
      str('4'),
      key('b'),
      str('5'),
      OE,
      OS,
      key('b'),
      str('6'),
      OE,
      AE,
      Ev.end,
    ])
    assert.deepEqual(run('null', ['a', 'b', 'a'], rows.slice(1, 2)), [
      AS,
      OS,
      key('b'),
      str('5'),
      key('a'),
      Ev.null,
      OE,
      AE,
      Ev.end,
    ])
    // Three columns with one label: the middle one wins when the last is
    // absent.
    assert.deepEqual(run('skip', ['a', 'a', 'a'], [[s('1'), s('2'), Cell.missing]]), [
      AS,
      OS,
      key('a'),
      str('2'),
      OE,
      AE,
      Ev.end,
    ])
  })

  it("protocol errors match the CSV renderer's", () => {
    const r = new RecordsToJson(new EventRecorder())
    const err = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([s('x')])))
    assert.equal(err.committedOutput, false)
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(END))

    r.tableEvent(schema(['a']))
    const err2 = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(schema(['a'])))
    assert.equal(err2.committedOutput, true)
    const err3 = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([s('x'), s('y')])))
    assert.match(err3.message, /row 1 has 2 cells/)
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([])))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([{ type: 'date' } as any])))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([undefined as any])))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(undefined as any))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(null as any))

    r.tableEvent(END)
    for (const ev of [END, row([s('x')]), schema(['a'])]) {
      const e = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(ev))
      assert.equal(e.committedOutput, true)
    }
    assert.equal(r.rows(), 0)
    assert.deepEqual(r.intoInner().events, [AS, AE, Ev.end])
  })

  it('a sink that fails on the end leaves the stage not done, and its failure reaches the caller', () => {
    const r = new RecordsToJson(
      new FnSink((ev) => {
        if ('end' === ev.type) throw Fail.output('closed')
      }),
    )
    r.tableEvent(schema(['a']))
    const err = fails('OUTPUT_FAILED', () => r.tableEvent(END))
    assert.equal(err.message, 'closed')
    assert.equal(r.isDone(), false)
  })

  it('keys are made once per schema, not per row', () => {
    const seen: JsonEvent[] = []
    const r = new RecordsToJson(
      new FnSink((ev) => {
        if ('key' === ev.type) seen.push(ev)
      }),
    )
    r.tableEvent(schema(['first', 'second']))
    const cells = [s('x'), Cell.null]
    r.tableEvent(row(cells))
    r.tableEvent(row(cells))
    assert.equal(seen.length, 4)
    assert.equal(seen[0], seen[2], 'the same event object on every row')
    assert.equal(seen[1], seen[3])
    assert.ok(Object.isFrozen(seen[0]))
  })

  it('a stop from the sink stops the row', () => {
    let seen = 0
    const r = new RecordsToJson(
      new FnSink(() => {
        seen++
        return 3 === seen ? 'stop' : 'continue'
      }),
    )
    assert.equal(r.tableEvent(schema(['a', 'b'])), 'continue')
    assert.equal(r.tableEvent(row([s('x'), s('y')])), 'stop')
    assert.equal(seen, 3, 'the array start, the object start, the first key, then no more')
    assert.equal(r.rows(), 0)
  })

  it('records render as JSON text', () => {
    const r = new RecordsToJson(new JsonRenderer(new StringOut()))
    r.tableEvent(schema(['name', 'age']))
    r.tableEvent(row([s('ada'), Cell.number(36, '36')]))
    r.tableEvent(row([s('lin'), Cell.missing]))
    r.tableEvent(END)
    assert.equal(r.rows(), 2)
    assert.equal(r.intoInner().intoInner().asStr(), '[{"name":"ada","age":36},{"name":"lin"}]')
  })

  it('refuses a missing policy it does not know', () => {
    assert.throws(() => new RecordsToJson(new EventRecorder()).withMissing('drop' as any), TypeError)
  })
})
