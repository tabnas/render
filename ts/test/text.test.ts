/* Copyright (c) 2026 tabnas, MIT License */

// The text outputs beside the fixtures: what a row cannot record. Ported
// from the tests in rs/src/text.rs: coalescing at the budget, the limit
// failing before the write, committed output and the short-write
// accounting, joins with empty items, replacements split at every
// boundary.

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Fail, Limits, Metrics } from '@tabnas/transduce'

import {
  BytesWriter,
  Concat,
  DEFAULT_BUDGET,
  Join,
  ReplaceText,
  StringOut,
  TextOut,
  WriteOut,
  Writer,
  hasCommitted,
} from '../dist/render'

// A writer that records each `write` as one chunk, so coalescing is
// observable, and fails after a set number of bytes when asked. It takes a
// buffer whole or refuses it whole: the all-or-nothing writer.
class Chunks implements Writer {
  chunks: string[] = []
  flushes = 0
  failAfter: number | null

  constructor(failAfter: number | null = null) {
    this.failAfter = failAfter
  }

  write(bytes: Uint8Array): number {
    const soFar = this.chunks.reduce((n, c) => n + Buffer.byteLength(c), 0)
    if (null != this.failAfter && soFar + bytes.length > this.failAfter) throw new Error('disk full')
    this.chunks.push(Buffer.from(bytes).toString('utf8'))
    return bytes.length
  }

  flush(): void {
    this.flushes++
  }
}

// A writer with room for `room` bytes that takes what fits of each write
// and fails once it is full: the short write before "no space left".
class Cramped implements Writer {
  room: number
  taken: number[] = []

  constructor(room: number) {
    this.room = room
  }

  write(bytes: Uint8Array): number {
    const left = this.room - this.taken.length
    if (0 === left) throw new Error('disk full')
    const n = Math.min(bytes.length, left)
    this.taken.push(...bytes.subarray(0, n))
    return n
  }

  text(): string {
    return Buffer.from(this.taken).toString('utf8')
  }
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

describe('WriteOut', () => {
  it('the default budget is 32 KiB', () => {
    assert.equal(DEFAULT_BUDGET, 32768)
  })

  it('fragments coalesce up to the budget and the buffer never exceeds it', () => {
    const out = new WriteOut(new Chunks()).withBudget(8)
    out.writeStr('abc')
    out.writeStr('def')
    out.writeStr('gh')
    // Exactly the budget: still held.
    const w = (out as any).writer as Chunks
    assert.deepEqual(w.chunks, [])
    out.writeStr('i')
    assert.deepEqual(w.chunks, ['abcdefgh'])
    assert.equal(out.committed(), 8)
    assert.equal(out.accepted(), 9)
    // A fragment at least as large as the budget bypasses the buffer, after
    // what was buffered before it.
    out.writeStr('0123456789')
    assert.deepEqual(w.chunks, ['abcdefgh', 'i', '0123456789'])
    out.writeStr('z')
    out.flush()
    const back = out.intoInner()
    assert.equal(back.chunks.join(''), 'abcdefghi0123456789z')
    assert.equal(back.flushes, 1)
  })

  it('the budget counts UTF-8 bytes, not string length', () => {
    const out = new WriteOut(new Chunks()).withBudget(4)
    out.writeStr('é') // 2 bytes
    out.writeStr('é') // 4: exactly the budget, held
    assert.deepEqual(out.intoInner().chunks, [])
    const out2 = new WriteOut(new Chunks()).withBudget(4)
    out2.writeStr('🚀') // 4 bytes, two code units: at the budget, written directly
    assert.deepEqual(out2.intoInner().chunks, ['🚀'])
  })

  it('a zero budget writes every fragment as it arrives, and no empty one', () => {
    const out = new WriteOut(new Chunks()).withBudget(0)
    out.writeStr('a')
    out.writeStr('')
    out.writeStr('bc')
    assert.deepEqual(out.intoInner().chunks, ['a', 'bc'])
  })

  it('the output limit fails before the fragment that would exceed it', () => {
    const metrics = new Metrics()
    const out = new WriteOut(new Chunks())
      .withBudget(4)
      .withLimits(Limits.with({ max_output_bytes: 10 }))
      .withMetrics(metrics)
    out.writeStr('hello')
    out.writeStr('worl')
    const err = fails('RESOURCE_LIMIT_EXCEEDED', () => out.writeStr('d!'))
    assert.deepEqual(err.limit, { name: 'max_output_bytes', value: 10 })
    // "hello" crossed the budget when "worl" arrived, so it was written
    // before the failure and the failure says so.
    assert.equal(err.committedOutput, true)
    assert.equal(out.accepted(), 9)
    assert.equal(out.committed(), 9)
    assert.equal(out.intoInner().chunks.join(''), 'helloworl')
    assert.equal(metrics.output_bytes, 9)
  })

  it('the output limit counts UTF-8 bytes', () => {
    const out = new WriteOut(new Chunks()).withLimits(Limits.with({ max_output_bytes: 3 }))
    fails('RESOURCE_LIMIT_EXCEEDED', () => out.writeStr('🚀'))
    const ok = new WriteOut(new Chunks()).withLimits(Limits.with({ max_output_bytes: 4 }))
    ok.writeStr('🚀')
    assert.equal(ok.accepted(), 4)
  })

  it('intoInner after a failure hands back exactly the committed bytes', () => {
    const out = new WriteOut(new Chunks()).withBudget(100).withLimits(Limits.with({ max_output_bytes: 5 }))
    out.writeStr('abc')
    const err = fails('RESOURCE_LIMIT_EXCEEDED', () => out.writeStr('xyz'))
    assert.equal(err.committedOutput, false)
    assert.equal(out.committed(), 0)
    const w = out.intoInner()
    assert.deepEqual(w.chunks, [], 'no committed output means none')
    assert.equal(w.flushes, 0)
  })

  it('a caller that wants the partial output flushes before intoInner', () => {
    const out = new WriteOut(new Chunks()).withBudget(100)
    out.writeStr('abc')
    out.flush()
    const w = out.intoInner()
    assert.deepEqual(w.chunks, ['abc'])
    assert.equal(w.flushes, 1)
  })

  it('a limit failure with nothing written is not committed', () => {
    const out = new WriteOut(new Chunks()).withLimits(Limits.with({ max_output_bytes: 3 }))
    const err = fails('RESOURCE_LIMIT_EXCEEDED', () => out.writeStr('abcd'))
    assert.equal(err.committedOutput, false)
    assert.deepEqual(out.intoInner().chunks, [])
  })

  it('a writer error is OUTPUT_FAILED and says whether bytes were committed', () => {
    const out = new WriteOut(new Chunks(4)).withBudget(3)
    out.writeStr('abc')
    assert.equal(out.committed(), 3)
    out.writeStr('de')
    assert.equal(out.committed(), 3)
    const err = fails('OUTPUT_FAILED', () => out.flush())
    assert.equal(err.committedOutput, true)
    assert.match(err.message, /disk full/)

    const out2 = new WriteOut(new Chunks(0)).withBudget(0)
    const err2 = fails('OUTPUT_FAILED', () => out2.writeStr('x'))
    assert.equal(err2.committedOutput, false)
  })

  it('a short write before the failure counts the bytes the writer took', () => {
    // Buffered, then flushed: the writer takes three bytes of the six and
    // fails on the rest.
    const metrics = new Metrics()
    const out = new WriteOut(new Cramped(3)).withBudget(100).withMetrics(metrics)
    out.writeStr('abc')
    out.writeStr('def')
    assert.equal(out.committed(), 0, 'still buffered')
    const err = fails('OUTPUT_FAILED', () => out.flush())
    assert.match(err.message, /disk full/)
    assert.equal(err.committedOutput, true, 'three bytes reached the writer before it failed')
    assert.equal(out.hasCommitted(), true)
    assert.equal(out.committed(), 3)
    assert.equal(out.accepted(), 6)
    assert.equal(metrics.output_bytes, 3)
    assert.equal(out.intoInner().text(), 'abc', 'the writer holds exactly committed() bytes')

    // Written directly: a fragment as large as the budget takes the same
    // path and is counted the same way.
    const direct = new WriteOut(new Cramped(2)).withBudget(0)
    const err2 = fails('OUTPUT_FAILED', () => direct.writeStr('abcdef'))
    assert.equal(err2.committedOutput, true)
    assert.equal(direct.committed(), 2)
    assert.equal(direct.accepted(), 0, 'the fragment was not accepted')
    assert.equal(direct.intoInner().text(), 'ab')

    // No room at all: nothing was taken, and the failure says so.
    const none = new WriteOut(new Cramped(0)).withBudget(0)
    const err3 = fails('OUTPUT_FAILED', () => none.writeStr('abc'))
    assert.equal(err3.committedOutput, false)
    assert.equal(none.hasCommitted(), false)
    assert.equal(none.committed(), 0)
    assert.equal(none.intoInner().text(), '')
  })

  it('a writer that takes nothing is a failure, not a spin', () => {
    const out = new WriteOut({ write: () => 0 }).withBudget(0)
    const err = fails('OUTPUT_FAILED', () => out.writeStr('abc'))
    assert.match(err.message, /failed to write whole buffer/)
    assert.equal(err.committedOutput, false)
    assert.equal(out.committed(), 0)
  })

  it('a buffer handed to the writer is never reused', () => {
    const w = new BytesWriter()
    const out = new WriteOut(w).withBudget(4)
    out.writeStr('abcd')
    out.writeStr('efgh')
    out.writeStr('ijkl')
    out.flush()
    assert.deepEqual(
      w.chunks.map((c) => Buffer.from(c).toString()),
      ['abcd', 'efgh', 'ijkl'],
    )
    assert.equal(w.text(), 'abcdefghijkl')
  })

  it('metrics count bytes handed to the writer', () => {
    const metrics = new Metrics()
    const out = new WriteOut(new BytesWriter()).withBudget(100).withMetrics(metrics)
    out.writeStr('twelve bytes')
    assert.equal(metrics.output_bytes, 0)
    out.flush()
    assert.equal(metrics.output_bytes, 12)
    assert.equal(out.intoInner().text(), 'twelve bytes')
  })

  it('a writer whose flush fails is OUTPUT_FAILED', () => {
    const out = new WriteOut({
      write: (b: Uint8Array) => b.length,
      flush: () => {
        throw new Error('pipe closed')
      },
    })
    out.writeStr('x')
    const err = fails('OUTPUT_FAILED', () => out.flush())
    assert.match(err.message, /pipe closed/)
  })

  it('hasCommitted is answered by the destination, not the buffer', () => {
    const out = new WriteOut(new Chunks()).withBudget(100)
    assert.equal(out.hasCommitted(), false)
    out.writeStr('abc')
    assert.equal(out.hasCommitted(), false, 'buffered is not committed')
    out.flush()
    assert.equal(out.hasCommitted(), true)

    const s = new StringOut()
    assert.equal(s.hasCommitted(), false)
    s.writeStr('x')
    assert.equal(s.hasCommitted(), true, 'the string is the destination')

    // The combinators forward the question; a replacer's carry has gone
    // nowhere yet.
    const r = new ReplaceText(new Join(new WriteOut(new BytesWriter()).withBudget(100), ','), 'ab', '')
    r.writeStr('xa')
    assert.equal(r.hasCommitted(), false)
    r.flush()
    assert.equal(r.hasCommitted(), true)

    // An output that cannot tell is taken to have committed.
    const bare: TextOut = { writeStr() {}, flush() {} }
    assert.equal(hasCommitted(bare), true)
  })
})

describe('StringOut', () => {
  it('keeps the text', () => {
    const s = new StringOut()
    s.writeStr('a')
    s.writeStr('b')
    s.flush()
    assert.equal(s.asStr(), 'ab')
    assert.equal(s.intoString(), 'ab')
  })
})

describe('Join and Concat', () => {
  it('join separates items, not fragments', () => {
    const j = new Join(new StringOut(), ', ')
    j.itemStart()
    j.writeStr('a')
    j.writeStr('b')
    j.itemEnd()
    j.itemStart()
    j.writeStr('c')
    j.itemEnd()
    assert.equal(j.items(), 2)
    assert.equal(j.intoInner().asStr(), 'ab, c')
  })

  it('join counts empty items', () => {
    const j = new Join(new StringOut(), ',')
    for (let i = 0; i < 3; i++) {
      j.itemStart()
      j.itemEnd()
    }
    j.itemStart()
    j.writeStr('x')
    j.itemEnd()
    j.itemStart()
    j.itemEnd()
    assert.equal(j.intoInner().asStr(), ',,,x,')
  })

  it('join treats a fragment outside an item as an item', () => {
    const j = new Join(new StringOut(), '|')
    j.writeStr('a')
    j.writeStr('')
    j.writeStr('b')
    j.flush()
    assert.equal(j.intoInner().asStr(), 'a||b')
  })

  it('join with no items writes nothing', () => {
    const j = new Join(new StringOut(), ',')
    j.flush()
    assert.equal(j.intoInner().asStr(), '')
  })

  it('join rejects unbalanced item markers', () => {
    const j = new Join(new StringOut(), ',')
    fails('PROTOCOL_ORDER_ERROR', () => j.itemEnd())
    j.itemStart()
    fails('PROTOCOL_ORDER_ERROR', () => j.itemStart())
  })

  it('concat appends items and fragments with nothing between them', () => {
    const c = new Concat(new StringOut())
    c.itemStart()
    c.writeStr('a')
    c.writeStr('b')
    c.itemEnd()
    c.itemStart()
    c.itemEnd()
    c.writeStr('c')
    c.flush()
    assert.equal(c.items(), 3)
    assert.equal(c.hasCommitted(), true)
    assert.equal(c.intoInner().asStr(), 'abc')
  })

  it("concat keeps join's item discipline", () => {
    const c = new Concat(new WriteOut(new BytesWriter()))
    fails('PROTOCOL_ORDER_ERROR', () => c.itemEnd())
    c.itemStart()
    fails('PROTOCOL_ORDER_ERROR', () => c.itemStart())
    c.writeStr('x')
    assert.equal(c.hasCommitted(), false, 'buffered beneath, not yet written')
    c.itemEnd()
    c.flush()
    assert.equal(c.intoInner().intoInner().text(), 'x')
  })
})

describe('ReplaceText', () => {
  // Feed `text` to a replacer split at `at`, then flushed.
  function replacedSplit(text: string, at: number, from: string, to: string): string {
    const r = new ReplaceText(new StringOut(), from, to)
    r.writeStr(text.slice(0, at))
    r.writeStr(text.slice(at))
    r.flush()
    return r.intoInner().intoString()
  }

  // The character boundaries of `text`, in code units: never inside a
  // surrogate pair.
  function boundaries(text: string): number[] {
    const out = [0]
    let i = 0
    for (const ch of text) {
      i += ch.length
      out.push(i)
    }
    return out
  }

  it('matches replaceAll when split at every boundary', () => {
    const cases: [string, string, string][] = [
      ['abcabc', 'abc', 'X'],
      ['xxabcxxabcxx', 'abc', ''],
      ['aaaa', 'aa', 'b'],
      ['aaaaa', 'aa', 'b'],
      ['ababab', 'aba', '_'],
      ['no match here', 'zzz', 'Y'],
      ['abab', 'abab', '1'],
      ['ab', 'abc', '1'],
      ['héllo wörld héllo', 'héllo', 'hi'],
      ['日本語日本', '日本', '*'],
      ['a\r\nb\r\n', '\r\n', '\n'],
      ['🚀x🚀🚀y', '🚀🚀', '!'],
      ['a🚀b', 'a🚀', '-'],
    ]
    for (const [text, from, to] of cases) {
      const want = text.replaceAll(from, to)
      for (const at of boundaries(text)) {
        assert.equal(replacedSplit(text, at, from, to), want, `${JSON.stringify(text)} split at ${at}`)
      }
    }
  })

  it('replaces across many one-character fragments', () => {
    const text = 'the cat sat on the mat with the hat'
    const r = new ReplaceText(new StringOut(), 'the', 'a')
    for (const c of text) r.writeStr(c)
    r.flush()
    assert.equal(r.intoInner().asStr(), text.replaceAll('the', 'a'))
  })

  it('never carries more than the literal less one character', () => {
    const r = new ReplaceText(new StringOut(), 'abcd', '')
    r.writeStr('xxabc')
    assert.equal(r.carry(), 'abc')
    assert.equal(r.intoInner().asStr(), 'xx')
    r.writeStr('ab')
    assert.equal(r.carry(), 'ab')
    assert.equal(r.intoInner().asStr(), 'xxabc')
    r.writeStr('cdab')
    assert.equal(r.carry(), 'ab')
    assert.equal(r.intoInner().asStr(), 'xxabc')
    r.flush()
    assert.equal(r.carry(), '')
    assert.equal(r.intoInner().asStr(), 'xxabcab')
  })

  it('never splits a surrogate pair into the carry', () => {
    const r = new ReplaceText(new StringOut(), '🚀🚀', '!')
    r.writeStr('a🚀')
    assert.equal(r.carry(), '🚀')
    assert.equal(r.intoInner().asStr(), 'a')
  })

  it('with an empty literal passes text through', () => {
    const r = new ReplaceText(new StringOut(), '', 'X')
    r.writeStr('abc')
    r.flush()
    assert.equal(r.intoInner().asStr(), 'abc')
  })

  it('flushes the carry at flush, so a later match cannot span it', () => {
    const r = new ReplaceText(new StringOut(), 'ab', 'X')
    r.writeStr('a')
    r.flush()
    r.writeStr('b')
    r.flush()
    assert.equal(r.intoInner().asStr(), 'ab')
  })

  it('combinators stack over a writer', () => {
    const inner = new WriteOut(new BytesWriter()).withBudget(3)
    const j = new Join(new ReplaceText(inner, '-', '+'), ';')
    j.writeStr('a-b')
    j.writeStr('c-')
    j.flush()
    assert.equal(j.intoInner().intoInner().intoInner().text(), 'a+b;c+')
  })
})
