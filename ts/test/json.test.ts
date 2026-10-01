/* Copyright (c) 2026 tabnas, MIT License */

// The JSON renderer beside the fixtures, ported from rs/src/json.rs: every
// JSON protocol error, the escaping (and that it agrees with transduce's),
// strings streamed in runs, and what a row cannot record (committed output,
// the text a rejected number leaves, a failed flush).

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Ev, Fail, JsonEvent, jsonString } from '@tabnas/transduce'

import { BytesWriter, JsonOptions, JsonRenderer, StringOut, TextOut, WriteOut } from '../dist/render'

function num(lexeme: string): JsonEvent {
  const v = Number(lexeme)
  return Ev.number(Number.isNaN(v) ? 0 : v, lexeme)
}

const value = (v: number): JsonEvent => Ev.number(v)
const { objectStart: OS, objectEnd: OE, arrayStart: AS, arrayEnd: AE, end: END } = Ev
const key = Ev.key
const str = Ev.string

function render(options: Partial<JsonOptions>, events: JsonEvent[]): string {
  const r = new JsonRenderer(new StringOut(), options)
  for (const ev of events) r.event(ev)
  return r.intoInner().intoString()
}

function compact(events: JsonEvent[]): string {
  return render({}, events)
}

function indented(n: number, events: JsonEvent[]): string {
  return render({ indent: n, trailingNewline: false }, events)
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

const DOC: JsonEvent[] = [
  OS,
  key('a'),
  AS,
  Ev.number(1, '1'),
  Ev.number(2.5),
  str('x'),
  Ev.bool(true),
  Ev.null,
  AE,
  key('b'),
  OS,
  OE,
  key('c'),
  AS,
  AE,
  key('d'),
  OS,
  key('e'),
  Ev.bool(false),
  OE,
  OE,
  END,
]

describe('JSON: layout', () => {
  it('compact output has no whitespace', () => {
    assert.equal(compact(DOC), '{"a":[1,2.5,"x",true,null],"b":{},"c":[],"d":{"e":false}}')
  })

  it('an indent writes fixed nesting and keeps empty containers on one line', () => {
    assert.equal(
      indented(2, DOC),
      '{\n  "a": [\n    1,\n    2.5,\n    "x",\n    true,\n    null\n  ],\n  "b": {},\n  "c": [],\n  "d": {\n    "e": false\n  }\n}',
    )
    assert.equal(indented(4, [AS, AS, Ev.null, AE, AE, END]), '[\n    [\n        null\n    ]\n]')
  })

  it('an indent of zero, or null, is compact', () => {
    assert.equal(indented(0, DOC), compact(DOC))
    assert.equal(render({ indent: null }, DOC), compact(DOC))
  })

  it('the trailing newline is written at the end when asked', () => {
    assert.equal(render({ trailingNewline: true }, [Ev.null, END]), 'null\n')
    assert.equal(compact([Ev.null, END]), 'null')
  })

  it('a root scalar is a document', () => {
    assert.equal(compact([str('x'), END]), '"x"')
    assert.equal(compact([num('-0.5e3'), END]), '-0.5e3')
    assert.equal(compact([Ev.bool(false), END]), 'false')
  })

  it('the defaults are compact with no trailing newline', () => {
    assert.deepEqual(JsonOptions.default(), { indent: null, trailingNewline: false })
  })
})

describe('JSON: strings', () => {
  it('are escaped as RFC 8259 requires and no more', () => {
    const text = 'q" b\\ n\n r\r t\t bs\u0008 ff\u000c nul\0 c1\u0001 us\u001f del\u007f é 日本 🚀 /  '
    assert.equal(
      compact([str(text), END]),
      '"q\\" b\\\\ n\\n r\\r t\\t bs\\b ff\\f nul\\u0000 c1\\u0001 us\\u001f del\u007f é 日本 🚀 /  "',
    )
    assert.equal(compact([OS, key('k"\n'), Ev.null, OE, END]), '{"k\\"\\n":null}')
  })

  it('escape exactly as transduce does', () => {
    let everyControl = ''
    for (let c = 0; c < 0x20; c++) everyControl += String.fromCharCode(c) + 'x'
    for (const text of [
      '',
      'plain',
      '"',
      '\\',
      '"\\"\\',
      'a"b\\c\nd',
      everyControl,
      'é 日本 🚀 \u007f \u0080   ￿',
      'ends with control \u0001',
      '\u0001 starts with control',
    ]) {
      assert.equal(compact([str(text), END]), jsonString(text), JSON.stringify(text))
    }
  })

  it('are streamed in runs and never copied whole', () => {
    class Fragments implements TextOut {
      parts: string[] = []
      writeStr(s: string): void {
        this.parts.push(s)
      }
      flush(): void {}
    }
    const r = new JsonRenderer(new Fragments())
    r.event(str('ab"cd\n\u0001ef'))
    assert.deepEqual(r.intoInner().parts, ['"', 'ab', '\\"', 'cd', '\\n', '\\u0001', 'ef', '"'])
    // A long string of control characters, six bytes of output each.
    const big = '\u0001'.repeat(64 * 1024)
    const r2 = new JsonRenderer(new StringOut())
    r2.event(str(big))
    assert.equal(r2.intoInner().asStr().length, big.length * 6 + 2)
  })
})

describe('JSON: numbers', () => {
  it('keep their lexeme or take the shortest form', () => {
    assert.equal(
      compact([
        AS,
        num('1.00'),
        num('123456789012345678901234567890'),
        num('-0'),
        num('1E+2'),
        value(0),
        value(1e21),
        value(0.1),
        value(-2),
        value(-0),
        AE,
        END,
      ]),
      '[1.00,123456789012345678901234567890,-0,1E+2,0,1e21,0.1,-2,-0]',
    )
    assert.equal(
      compact([AS, value(1e300), value(1e-300), value(1.5e17), value(1e20), AE, END]),
      '[1e300,1e-300,150000000000000000,100000000000000000000]',
    )
  })

  it('a lexeme that is not a JSON number is INVALID_NUMBER', () => {
    for (const bad of ['1.', '01', 'NaN', '+1', '0x1']) {
      const err = fails('INVALID_NUMBER', () => compact([AS, num(bad), AE, END]))
      assert.equal(err.committedOutput, true)
    }
    const err = fails('INVALID_NUMBER', () => compact([num('1.'), END]))
    assert.equal(err.committedOutput, false)
  })

  it('NaN and infinity are unrepresentable', () => {
    for (const v of [NaN, Infinity, -Infinity]) {
      fails('TARGET_VALUE_UNREPRESENTABLE', () => compact([value(v), END]))
    }
  })

  it('an overflowed lexeme is unrepresentable too', () => {
    const overflowed = Ev.number(Infinity, '1e999')
    const r = new JsonRenderer(new StringOut())
    r.event(AS)
    r.event(num('1'))
    const err = fails('TARGET_VALUE_UNREPRESENTABLE', () => r.event(overflowed))
    assert.equal(err.committedOutput, true)
    assert.equal(r.intoInner().asStr(), '[1')
    const err2 = fails('TARGET_VALUE_UNREPRESENTABLE', () => compact([overflowed, END]))
    assert.equal(err2.committedOutput, false)
  })

  it('a rejected number leaves no separator behind', () => {
    const r = new JsonRenderer(new StringOut())
    r.event(AS)
    r.event(num('1'))
    fails('INVALID_NUMBER', () => r.event(num('01')))
    assert.equal(r.intoInner().asStr(), '[1')
    fails('TARGET_VALUE_UNREPRESENTABLE', () => r.event(value(NaN)))
    assert.equal(r.intoInner().asStr(), '[1')
    // A caller that carries on regardless still gets a document.
    for (const ev of [value(2), AE, END]) r.event(ev)
    assert.equal(r.intoInner().asStr(), '[1,2]')
  })
})

describe('JSON: protocol errors', () => {
  function protocolError(events: JsonEvent[]): Fail {
    return fails('PROTOCOL_ORDER_ERROR', () => compact(events))
  }

  it('a second root is a protocol error', () => {
    protocolError([Ev.null, Ev.null])
    protocolError([OS, OE, AS])
    protocolError([str('a'), str('b'), END])
  })

  it('an end without a complete root is a protocol error', () => {
    assert.equal(protocolError([END]).committedOutput, false)
    protocolError([AS, END])
    protocolError([OS, key('a'), END])
    protocolError([OS, key('a'), Ev.null, END])
  })

  it('a key outside an object or where a value is due is a protocol error', () => {
    protocolError([key('a')])
    protocolError([AS, key('a')])
    protocolError([OS, key('a'), key('b')])
    protocolError([Ev.null, key('a')])
  })

  it('a value where a key is due is a protocol error', () => {
    protocolError([OS, Ev.null])
    protocolError([OS, AS])
    protocolError([OS, key('a'), Ev.null, str('b')])
  })

  it('an unbalanced or mismatched close is a protocol error', () => {
    protocolError([OE])
    protocolError([AE])
    protocolError([AS, OE])
    protocolError([OS, AE])
    protocolError([OS, key('a'), OE])
    protocolError([AS, AE, AE])
  })

  it('anything after the end is a protocol error', () => {
    for (const after of [END, Ev.null, key('a'), OS, OE, AE]) {
      assert.equal(protocolError([Ev.null, END, after]).committedOutput, true)
    }
  })

  it('an event the protocol does not define is a protocol error', () => {
    protocolError([{ type: 'comment' } as any])
    protocolError([undefined as any])
    protocolError([null as any])
    protocolError([AS, { type: 'string', value: 1 } as any])
    protocolError([OS, { type: 'key', key: 1 } as any])
  })

  it('a failure leaves the output a prefix of the document', () => {
    const r = new JsonRenderer(new StringOut())
    for (const ev of [OS, key('a'), AS, Ev.null]) r.event(ev)
    assert.equal(r.depth(), 2)
    fails('PROTOCOL_ORDER_ERROR', () => r.event(key('b')))
    assert.equal(r.intoInner().asStr(), '{"a":[null')
  })
})

describe('JSON: the output', () => {
  it('a failed flush at the end leaves the renderer not done', () => {
    const out = new WriteOut({
      write: (b: Uint8Array) => b.length,
      flush: () => {
        throw new Error('pipe closed')
      },
    })
    const r = new JsonRenderer(out)
    r.event(Ev.null)
    fails('OUTPUT_FAILED', () => r.event(END))
    assert.equal(r.isDone(), false)
  })

  it('committed output means bytes that reached the writer', () => {
    const r = new JsonRenderer(new WriteOut(new BytesWriter()))
    r.event(AS)
    const err = fails('PROTOCOL_ORDER_ERROR', () => r.event(key('k')))
    assert.equal(err.committedOutput, false, 'the bracket is only buffered')
    assert.equal(r.intoInner().committed(), 0)

    const r2 = new JsonRenderer(new WriteOut(new BytesWriter()).withBudget(0))
    r2.event(AS)
    const err2 = fails('PROTOCOL_ORDER_ERROR', () => r2.event(key('k')))
    assert.equal(err2.committedOutput, true, 'the bracket reached the writer')
  })

  it('the end flushes the output and nothing else does', () => {
    const r = new JsonRenderer(new WriteOut(new BytesWriter()))
    for (const ev of [AS, Ev.bool(true), AE]) assert.equal(r.event(ev), 'continue')
    assert.equal(r.intoInner().committed(), 0)
    assert.equal(r.isDone(), false)
    r.event(END)
    assert.equal(r.isDone(), true)
    assert.equal(r.intoInner().committed(), 6)
    assert.equal(r.intoInner().intoInner().text(), '[true]')
  })
})
