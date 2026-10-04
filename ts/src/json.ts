/* Copyright (c) 2026 tabnas, MIT License */

// `JsonEvents/1` as JSON text.
//
// The renderer writes what it is given, in the order it is given, with
// nothing held back but the separators: a comma is written when the next
// item begins, never speculatively, so an aborted document is still a
// prefix of a valid one. Compact output is the standard profile; a fixed
// indent is a separate profile for people rather than programs, and the
// two differ only in whitespace. Strings are escaped as RFC 8259 requires
// and no more: `"`, `\`, and the control characters, with everything else,
// non-ASCII, DEL and U+2028 included, written as itself, because the
// output is UTF-8 and an escape would only make it longer.
//
// The renderer validates the event sequence as it goes, because a
// third-party source is as much a source of `JsonEvents/1` as the standard
// ones are: one root value, keys only where a member begins, balanced
// containers, one end.

import { Fail, Flow, JsonEvent, JsonOptions, Sink, TextOut } from '@tabnas/alchemy/shared'

import { checkNumber, writeValue } from './number'
import { hasCommitted } from './text'

type Frame = { object: true; first: boolean; expectingKey: boolean } | { object: false; first: boolean }

// The `\u00XX` forms of the control characters, the short escapes RFC 8259
// names among them, indexed by code point.
const CONTROL: readonly string[] = Object.freeze(
  Array.from({ length: 0x20 }, (_, c) => {
    switch (c) {
      case 0x08:
        return '\\b'
      case 0x09:
        return '\\t'
      case 0x0a:
        return '\\n'
      case 0x0c:
        return '\\f'
      case 0x0d:
        return '\\r'
      default:
        return '\\u' + c.toString(16).padStart(4, '0')
    }
  }),
)

// The escape RFC 8259 requires for the code unit `c`, or `undefined` when
// it is written as itself.
function escape(c: number): string | undefined {
  if (0x22 === c) return '\\"'
  if (0x5c === c) return '\\\\'
  if (c < 0x20) return CONTROL[c]
  return undefined
}

// Renders `JsonEvents/1` as JSON text.
//
// Exactly one root value, then the end; a second root, an end before the
// root or with a container open, a key outside an object or where a value
// is due, a value where a key is due, an unbalanced or mismatched close,
// and any event after the end are `PROTOCOL_ORDER_ERROR`. Numbers write
// their lexeme when it is a JSON number (`INVALID_NUMBER` otherwise) and
// the shortest text that reads back as the value when there is none; NaN
// and infinity have no JSON form and are `TARGET_VALUE_UNREPRESENTABLE`,
// whatever lexeme stands beside them. A number is checked before its
// separator is written, so a rejected value leaves no trace. The output is
// flushed once, at the end; a failure found after any text was written
// says so with `committedOutput`.
export class JsonRenderer<O extends TextOut = TextOut> implements Sink {
  private out: O
  private opts: JsonOptions
  // Spaces per level; zero is compact.
  private indent: number
  private stack: Frame[] = []
  private rootDone = false
  private ended = false
  private emitted = false
  // Spaces, grown to the widest indentation written so far.
  private pad = ''

  constructor(out: O, options?: Partial<JsonOptions>) {
    const opts: JsonOptions = { ...JsonOptions.default(), ...(options ?? {}) }
    const indent = opts.indent ?? 0
    if (!Number.isInteger(indent) || indent < 0) {
      throw new TypeError(`an indent is a count of spaces, not ${opts.indent}`)
    }
    this.out = out
    this.opts = Object.freeze(opts)
    this.indent = indent
  }

  options(): Readonly<JsonOptions> {
    return this.opts
  }

  // Containers currently open.
  depth(): number {
    return this.stack.length
  }

  // Whether the end has been rendered.
  isDone(): boolean {
    return this.ended
  }

  intoInner(): O {
    return this.out
  }

  // Mark a failure as leaving partial output when text this renderer wrote
  // has reached the destination; text still buffered in the output has
  // not, and the output knows which.
  private fail(f: Fail): Fail {
    return this.emitted && hasCommitted(this.out) ? f.committed() : f
  }

  private protocol(message: string): Fail {
    return this.fail(Fail.protocol(message))
  }

  private put(s: string): void {
    this.emitted = true
    this.out.writeStr(s)
  }

  // The escaped form of `s`, streamed: each run that needs no escaping is
  // written as it is and each escape as it comes, so the renderer never
  // holds an escaped copy of a scalar.
  private putString(s: string): void {
    this.put('"')
    let from = 0
    for (let i = 0; i < s.length; i++) {
      const escaped = escape(s.charCodeAt(i))
      if (undefined === escaped) continue
      if (i > from) this.put(s.slice(from, i))
      this.put(escaped)
      from = i + 1
    }
    if (from < s.length) this.put(0 === from ? s : s.slice(from))
    this.put('"')
  }

  // A line break and the indentation of `depth` levels; nothing when
  // compact.
  private breakLine(depth: number): void {
    if (0 === this.indent) return
    const width = depth * this.indent
    if (this.pad.length < width) this.pad = ' '.repeat(width)
    this.put('\n')
    this.put(this.pad.slice(0, width))
  }

  // The separators before a value, and the check that one may begin.
  private beginValue(): void {
    if (this.ended) throw this.protocol('a value after the end')
    const depth = this.stack.length
    const top = this.stack[depth - 1]
    if (undefined === top) {
      if (this.rootDone) throw this.protocol('a second root value')
      return
    }
    if (top.object) {
      if (top.expectingKey) throw this.protocol('a value where a key is due')
      return
    }
    const comma = !top.first
    top.first = false
    if (comma) this.put(',')
    this.breakLine(depth)
  }

  // Bookkeeping after a whole value.
  private endValue(): void {
    const top = this.stack[this.stack.length - 1]
    if (undefined === top) this.rootDone = true
    else if (top.object) top.expectingKey = true
  }

  private key(k: string): void {
    if (this.ended) throw this.protocol('a key after the end')
    const depth = this.stack.length
    const top = this.stack[depth - 1]
    if (undefined === top) throw this.protocol('a key outside an object')
    if (!top.object) throw this.protocol('a key inside an array')
    if (!top.expectingKey) throw this.protocol('a key where a value is due')
    const comma = !top.first
    top.first = false
    if (comma) this.put(',')
    this.breakLine(depth)
    this.putString(k)
    this.put(0 < this.indent ? ': ' : ':')
    // Only after the key is on the wire, so a write failure cannot leave
    // the frame half updated.
    top.expectingKey = false
  }

  private start(open: string, frame: Frame): void {
    this.beginValue()
    this.put(open)
    this.stack.push(frame)
  }

  private closeObject(): void {
    if (this.ended) throw this.protocol('an object end after the end')
    const top = this.stack[this.stack.length - 1]
    if (undefined === top) throw this.protocol('an object end with no open object')
    if (!top.object) throw this.protocol('an object end inside an array')
    if (!top.expectingKey) throw this.protocol('an object ended after a key with no value')
    this.stack.pop()
    if (!top.first) this.breakLine(this.stack.length)
    this.put('}')
    this.endValue()
  }

  private closeArray(): void {
    if (this.ended) throw this.protocol('an array end after the end')
    const top = this.stack[this.stack.length - 1]
    if (undefined === top) throw this.protocol('an array end with no open array')
    if (top.object) throw this.protocol('an array end inside an object')
    this.stack.pop()
    if (!top.first) this.breakLine(this.stack.length)
    this.put(']')
    this.endValue()
  }

  private scalar(ev: JsonEvent): void {
    // Before the separator: a number that will be refused must leave
    // nothing behind, or a caller that carries on after the failure would
    // find `[1,,2]` in the output.
    if ('number' === ev.type) {
      try {
        checkNumber(ev.value, ev.lexeme)
      } catch (err) {
        throw err instanceof Fail ? this.fail(err) : err
      }
    } else if ('bool' === ev.type && 'boolean' !== typeof ev.value) {
      throw this.protocol('a bool event without a boolean')
    } else if ('string' === ev.type && 'string' !== typeof ev.value) {
      throw this.protocol('a string event without a string')
    }
    this.beginValue()
    switch (ev.type) {
      case 'null':
        this.put('null')
        break
      case 'bool':
        this.put(ev.value ? 'true' : 'false')
        break
      case 'number':
        // `checkNumber` passed it: the lexeme as it is, or the value
        // formatted once.
        this.put(null != ev.lexeme ? ev.lexeme : writeValue(ev.value))
        break
      case 'string':
        this.putString(ev.value)
        break
    }
    this.endValue()
  }

  private end(): void {
    if (this.ended) throw this.protocol('a second end')
    if (0 < this.stack.length) {
      throw this.protocol(`the end with ${this.stack.length} open container(s)`)
    }
    if (!this.rootDone) throw this.protocol('the end before a root value')
    if (this.opts.trailingNewline) this.put('\n')
    // Ended only once the flush has succeeded: a document whose last bytes
    // never reached the writer is not done, whatever the end said.
    this.out.flush()
    this.ended = true
  }

  event(ev: JsonEvent): Flow {
    switch (ev?.type) {
      case 'object_start':
        this.start('{', { object: true, first: true, expectingKey: true })
        break
      case 'array_start':
        this.start('[', { object: false, first: true })
        break
      case 'object_end':
        this.closeObject()
        break
      case 'array_end':
        this.closeArray()
        break
      case 'key':
        if ('string' !== typeof ev.key) throw this.protocol('a key event without a string')
        this.key(ev.key)
        break
      case 'null':
      case 'bool':
      case 'number':
      case 'string':
        this.scalar(ev)
        break
      case 'end':
        this.end()
        break
      default:
        throw this.protocol(`not an event of JsonEvents/1: ${JSON.stringify((ev as any)?.type)}`)
    }
    return 'continue'
  }
}
