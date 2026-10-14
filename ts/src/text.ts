/* Copyright (c) 2026 tabnas, MIT License */

// Text output: where rendered fragments go.
//
// A renderer produces many small fragments (a quote, a field, a comma) and
// must never hold a whole document. `TextOut` is the boundary: a fragment
// in, a `Fail` thrown out. `WriteOut` coalesces fragments to a byte budget
// before they reach a `Writer`, so a renderer can write a character at a
// time without paying a system call for each, and it is where the output
// limit and the output-bytes metric live, because it is the one stage that
// knows what actually left. `Join` and `ReplaceText` are the two text
// combinators whose correctness depends on the difference between a
// logical item and a transport chunk, which is why they live beside the
// writer rather than in the language that uses them.
//
// Every count here is in UTF-8 bytes, never in string length: a JavaScript
// string is UTF-16, and `🚀` is two code units but four bytes on the wire.
// Fragments are expected to be well-formed strings; a lone surrogate is
// encoded, and counted, as U+FFFD, as `Buffer` encodes it.

import { writeSync } from 'node:fs'

import { Fail, Limits, Metrics, TextOut, Writer, utf8Bytes } from '@tabnas/alchemy/shared'

// What `out` says about committed text, `true` when it cannot tell.
// `TextOut`'s `hasCommitted` is optional: an output that leaves it out gets
// this conservative answer, since whatever a renderer handed over may be
// out.
export function hasCommitted(out: TextOut): boolean {
  return 'function' === typeof out.hasCommitted ? out.hasCommitted() : true
}

// The default coalescing budget of a `WriteOut`: large enough that a write
// per budget is negligible next to the parse, small enough to be invisible
// in a process's memory.
export const DEFAULT_BUDGET = 32 * 1024

// The default hold of a `WriteOut`: the longest record it keeps back until
// the record's end is marked. A record longer than this is written as it
// comes, so that one huge record (a document whose root holds one value)
// costs a bounded buffer rather than its whole size.
export const DEFAULT_HOLD = 16 * 1024 * 1024

// A `Writer` that keeps every chunk it is given, for tests and small
// results: the counterpart of writing into a `Vec<u8>`.
export class BytesWriter implements Writer {
  readonly chunks: Uint8Array[] = []

  write(bytes: Uint8Array): number {
    this.chunks.push(bytes)
    return bytes.length
  }

  // Everything written, as one buffer.
  bytes(): Buffer {
    return Buffer.concat(this.chunks)
  }

  // Everything written, as UTF-8 text.
  text(): string {
    return this.bytes().toString('utf8')
  }
}

// A `Writer` over a file descriptor (`1` for standard output), written
// synchronously, so the backpressure is the descriptor's own. A
// non-blocking descriptor that answers `EAGAIN` is offered the bytes
// again, as Rust retries `Interrupted`. It has no `flush`: `writeSync`
// holds nothing back.
export class FdWriter implements Writer {
  readonly fd: number

  constructor(fd: number) {
    this.fd = fd
  }

  write(bytes: Uint8Array): number {
    for (;;) {
      try {
        return writeSync(this.fd, bytes)
      } catch (err: any) {
        if ('EAGAIN' !== err?.code) throw err
      }
    }
  }
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

// Coalesces fragments and writes them to a `Writer`.
//
// Until a record end is marked (`recordEnd`), retention is bounded by the
// budget: the buffer never holds more than `budget` bytes, and a fragment
// at least as large as the budget goes to the writer directly, after
// whatever was buffered before it. From the first record end on, the
// buffer is drained only at a record end, once it holds at least `budget`
// bytes, so the text a run leaves behind when it fails part way ends with
// a whole record: a failure drops the buffer, and with it the record that
// was being written. A record that outgrows the hold (`DEFAULT_HOLD`
// unless `withHold` says) is written as it comes, the fragment that
// outgrows it going to the writer directly after the buffer, and a failure
// inside it cuts it; the buffer never holds more of an open record than the
// hold, so retention is bounded by the budget and the hold together.
// The output limit is checked on every fragment BEFORE it is accepted,
// counting the
// bytes buffered as well as the bytes written, so a run that would exceed
// `max_output_bytes` fails without emitting the fragment that crossed the
// line. `output_bytes` in the shared `Metrics` counts bytes the writer
// accepted, which is what "written to the output" means to a caller reading
// the metrics after a failure; like `committed()`, it is kept per `write`
// call, so the part of a buffer a writer took before failing is counted. A
// zero-length fragment is never a write.
export class WriteOut<W extends Writer = Writer> implements TextOut {
  private writer: W
  private buf: Buffer | null = null
  // Bytes in `buf`.
  private len = 0
  private budget = DEFAULT_BUDGET
  private hold = DEFAULT_HOLD
  private maxOutputBytes: number | null = null
  private metrics: Metrics | null = null
  // Bytes accepted: buffered or written.
  private acceptedBytes = 0
  // Bytes the writer accepted, counted write by write rather than buffer by
  // buffer, so a writer that took part of a buffer and then failed is
  // counted for the part it took. Nonzero means a later failure finds
  // committed output.
  private committedBytes = 0
  // Whether a record end has been marked: from the first, the buffer is
  // drained only at a record end.
  private records = false
  // Where the open record begins in the buffer: the bytes before it are
  // whole records.
  private mark = 0

  constructor(writer: W) {
    this.writer = writer
  }

  // The coalescing budget in bytes. Zero means every fragment is written as
  // it arrives, which is what a test of the writer's ordering wants; once
  // records are marked, it means every record is written as it ends.
  withBudget(budget: number): this {
    if (!Number.isInteger(budget) || budget < 0) {
      throw new TypeError(`a budget is a count of bytes, not ${budget}`)
    }
    this.budget = budget
    this.buf = null
    this.len = 0
    this.mark = 0
    return this
  }

  // The longest record, in bytes, held back until its end is marked; a
  // longer one is written as it comes. Zero holds nothing, so marks change
  // nothing.
  withHold(hold: number): this {
    if (!Number.isInteger(hold) || hold < 0) {
      throw new TypeError(`a hold is a count of bytes, not ${hold}`)
    }
    this.hold = hold
    return this
  }

  // Enforce `limits.max_output_bytes`; the other limits belong to the
  // stages upstream.
  withLimits(limits: Limits): this {
    this.maxOutputBytes = limits.max_output_bytes ?? null
    return this
  }

  // Count `output_bytes` into these metrics.
  withMetrics(metrics: Metrics): this {
    this.metrics = metrics
    return this
  }

  // Bytes accepted so far, buffered or written.
  accepted(): number {
    return this.acceptedBytes
  }

  // Bytes the writer accepted, including the bytes of a short write that a
  // failure cut off: after `OUTPUT_FAILED` this is what the writer holds,
  // not the buffers that were sent whole.
  committed(): number {
    return this.committedBytes
  }

  // Hand the writer back WITHOUT flushing. Whatever the buffer still holds
  // is dropped, so the writer holds exactly the bytes `committed()` counts,
  // a short write before a failure included: a document that failed before
  // its end does not reach the writer on the way out, which is what a
  // failure that reported no committed output promised the host. A
  // renderer flushes once, at its end, and a caller that wants a partial
  // output anyway calls `flush` first, knowingly.
  intoInner(): W {
    this.buf = null
    this.len = 0
    this.mark = 0
    return this.writer
  }

  private failIo(what: string): Fail {
    const f = Fail.output(`writing the output failed: ${what}`)
    return 0 < this.committedBytes ? f.committed() : f
  }

  // Write `bytes` as `write_all` does, but counting every write the writer
  // accepted before going on to the next. A writer may take part of a
  // buffer and then fail (a short write to a full disk, or up to a
  // file-size limit): a counter kept per buffer would then say the writer
  // received nothing while it holds the part it took. Counting per write
  // keeps `committed()` equal to what the writer holds, whichever write
  // failed. A write that takes nothing is a failure, not a spin.
  private send(bytes: Uint8Array): void {
    while (0 < bytes.length) {
      let n: number
      try {
        n = this.writer.write(bytes)
      } catch (err) {
        // The buffer is not retried: a failed writer is done, and the
        // caller learns how many bytes it accepted before that.
        this.len = 0
        throw this.failIo(message(err))
      }
      if (!(n > 0)) {
        this.len = 0
        throw this.failIo('failed to write whole buffer')
      }
      if (n > bytes.length) n = bytes.length
      this.committedBytes += n
      if (null != this.metrics) this.metrics.output_bytes += n
      bytes = bytes.subarray(n)
    }
  }

  // Send the whole buffer, which holds no open record afterwards: it is
  // empty whether the send succeeded or failed.
  private drain(): void {
    this.mark = 0
    if (0 === this.len || null == this.buf) return
    // The buffer goes to the writer, which may keep it; the next fragment
    // starts a fresh one.
    const pending = this.buf.subarray(0, this.len)
    this.buf = null
    this.len = 0
    this.send(pending)
  }

  writeStr(s: string): void {
    const len = utf8Bytes(s)
    if (null != this.maxOutputBytes && this.acceptedBytes + len > this.maxOutputBytes) {
      const max = this.maxOutputBytes
      const f = Fail.limit(
        'max_output_bytes',
        max,
        `the output would exceed ${max} bytes: ${this.acceptedBytes} written, ${len} more`,
      )
      throw 0 < this.committedBytes ? f.committed() : f
    }
    if (0 === len) return
    if (this.records) {
      // The open record is held until its end. One that outgrows the hold
      // is written as it comes: the buffer goes first, then the fragment
      // that outgrew it, which the buffer never takes, so the buffer never
      // grows past the hold.
      if (this.len - this.mark + len > this.hold) {
        this.drain()
        this.send(Buffer.from(s, 'utf8'))
      } else {
        this.append(s, len)
      }
      this.acceptedBytes += len
      return
    }
    if (this.len + len > this.budget) this.drain()
    if (len >= this.budget) {
      this.send(Buffer.from(s, 'utf8'))
    } else {
      if (null == this.buf) this.buf = Buffer.allocUnsafe(this.budget)
      this.len += this.buf.write(s, this.len, 'utf8')
    }
    this.acceptedBytes += len
  }

  // Add `s`, `len` bytes, to the buffer, which grows past the budget to
  // hold an open record.
  private append(s: string, len: number): void {
    const need = this.len + len
    if (null == this.buf || this.buf.length < need) {
      const size = Math.max(need, this.budget, null == this.buf ? 0 : 2 * this.buf.length)
      const grown = Buffer.allocUnsafe(size)
      if (null != this.buf && 0 < this.len) this.buf.copy(grown, 0, 0, this.len)
      this.buf = grown
    }
    this.len += this.buf.write(s, this.len, 'utf8')
  }

  // The buffer now ends with a whole record: it is drained if it holds the
  // budget, and kept otherwise, the open record starting after it.
  recordEnd(): void {
    this.records = true
    if (this.len >= this.budget) this.drain()
    else this.mark = this.len
  }

  flush(): void {
    this.drain()
    if ('function' === typeof this.writer.flush) {
      try {
        this.writer.flush()
      } catch (err) {
        throw this.failIo(message(err))
      }
    }
  }

  hasCommitted(): boolean {
    return 0 < this.committedBytes
  }
}

// A `TextOut` that keeps the text, for tests and small results.
export class StringOut implements TextOut {
  text = ''

  writeStr(s: string): void {
    this.text += s
  }

  flush(): void {}

  // The string is the destination, so its text is committed as soon as it
  // is there.
  hasCommitted(): boolean {
    return 0 < this.text.length
  }

  asStr(): string {
    return this.text
  }

  intoString(): string {
    return this.text
  }

  toString(): string {
    return this.text
  }
}

// Writes a separator between logical items.
//
// An item is what lies between `itemStart` and `itemEnd`; it may be
// written in any number of fragments, or in none, and an empty item is
// still an item, so `["", ""]` joined with `,` is `,`. A fragment written
// outside an item is an item of its own, which is the common case of one
// text per element and needs no markers. The separator goes before every
// item but the first, never between the fragments of one item: that
// distinction is the whole reason this type exists, because a chunked
// transport must not change the text.
export class Join<O extends TextOut = TextOut> implements TextOut {
  private out: O
  private separator: string
  private count = 0
  private inItem = false

  constructor(out: O, separator: string) {
    this.out = out
    this.separator = separator
  }

  // Begin an item: the separator is written now if an item came before.
  // Starting an item inside an item is `PROTOCOL_ORDER_ERROR`.
  itemStart(): void {
    if (this.inItem) {
      throw Fail.protocol('join: an item started inside an item that has not ended')
    }
    if (0 < this.count && '' !== this.separator) this.out.writeStr(this.separator)
    this.count++
    this.inItem = true
  }

  // End the current item. Ending when no item is open is
  // `PROTOCOL_ORDER_ERROR`.
  itemEnd(): void {
    if (!this.inItem) throw Fail.protocol('join: an item ended when none was open')
    this.inItem = false
  }

  // Items begun so far.
  items(): number {
    return this.count
  }

  intoInner(): O {
    return this.out
  }

  writeStr(s: string): void {
    if (this.inItem) {
      this.out.writeStr(s)
      return
    }
    this.itemStart()
    this.out.writeStr(s)
    this.itemEnd()
  }

  // Flushes the output beneath; an open item stays open, since a flush is
  // about transport and an item is about meaning.
  flush(): void {
    this.out.flush()
  }

  hasCommitted(): boolean {
    return hasCommitted(this.out)
  }

  // Passed on as it is: the separator of the next item is written when that
  // item starts, after the mark, so it belongs to the next record.
  recordEnd(): void {
    this.out.recordEnd?.()
  }
}

// Concatenation: a `Join` with no separator, under the name the design
// brief and the language give it.
//
// Every fragment is appended as it is. The item markers are accepted and
// counted all the same, so the interpreter's `concat` and `join` share one
// shape and a program can move between them without the calls around them
// changing; that shared shape is the whole reason for a type where a bare
// output would do.
export class Concat<O extends TextOut = TextOut> implements TextOut {
  private join: Join<O>

  constructor(out: O) {
    this.join = new Join(out, '')
  }

  // Begin an item; see `Join.itemStart`.
  itemStart(): void {
    this.join.itemStart()
  }

  // End the current item; see `Join.itemEnd`.
  itemEnd(): void {
    this.join.itemEnd()
  }

  // Items begun so far.
  items(): number {
    return this.join.items()
  }

  intoInner(): O {
    return this.join.intoInner()
  }

  writeStr(s: string): void {
    this.join.writeStr(s)
  }

  flush(): void {
    this.join.flush()
  }

  hasCommitted(): boolean {
    return this.join.hasCommitted()
  }

  recordEnd(): void {
    this.join.recordEnd()
  }
}

function isHighSurrogate(c: number): boolean {
  return c >= 0xd800 && c <= 0xdbff
}

// Replaces every occurrence of a fixed literal, across fragment boundaries.
//
// The text is treated as one string however it is chunked, with the same
// left-to-right, non-overlapping matches as `String#replaceAll` with a
// string pattern. To do that without holding the text, at most one
// character less than the literal is carried from one fragment to the
// next: the longest suffix of what has been seen that could still begin a
// match. `flush` writes the carry out, because nothing can complete it
// once the caller has declared the text at a boundary; the renderer's
// single flush at the end of a document is what makes that safe. An empty
// literal matches nothing and the text passes through unchanged, the one
// well-defined meaning it can have in a stream.
//
// A record end is no boundary for a match, so marks change no text. Record
// ends that arrive while code units are carried wait with them, each at its
// own offset, and are passed on where they fall once they are written:
// before a match that begins there, after one that ends there. One that
// falls inside a match is dropped, the two records it parted ending
// together at the next mark. At most one waits at each offset of the carry,
// so they are as bounded as the carry is.
export class ReplaceText<O extends TextOut = TextOut> implements TextOut {
  private out: O
  private from: string
  private to: string
  // The carried suffix; exposed read-only for tests of the bound.
  private pending = ''
  // Record ends at these offsets into the carry, in code units, ascending,
  // waiting for the carried text to be written.
  private marks: number[] = []

  constructor(out: O, from: string, to: string) {
    this.out = out
    this.from = from
    this.to = to
  }

  intoInner(): O {
    return this.out
  }

  // What is carried now, waiting to see whether it begins a match.
  carry(): string {
    return this.pending
  }

  // The longest proper prefix of the literal that `rest` ends with, in code
  // units; zero when there is none. A prefix that would split a surrogate
  // pair of the literal is skipped: a match cannot end inside a character.
  private pendingLen(rest: string): number {
    const max = Math.min(rest.length, this.from.length - 1)
    for (let k = max; k > 0; k--) {
      if (isHighSurrogate(this.from.charCodeAt(k - 1))) continue
      if (rest.endsWith(this.from.slice(0, k))) return k
    }
    return 0
  }

  // Write `s`, which begins `at` code units into the text being scanned,
  // with the waiting record ends from `marks[next]` on where they fall
  // within it, its end included. Answers the index of the first mark still
  // waiting.
  private emit(s: string, at: number, marks: number[], next: number): number {
    let from = 0
    for (; next < marks.length && marks[next] <= at + s.length; next++) {
      const cut = Math.max(marks[next] - at, from)
      const piece = s.slice(from, cut)
      if ('' !== piece) this.out.writeStr(piece)
      this.out.recordEnd?.()
      from = cut
    }
    const rest = s.slice(from)
    if ('' !== rest) this.out.writeStr(rest)
    return next
  }

  // Replace the matches in `text`, with record ends waiting at `marks`, and
  // carry what could still begin one.
  private scan(text: string, marks: number[]): void {
    let next = 0
    let rest = text
    let at = 0
    for (let i = rest.indexOf(this.from); -1 !== i; i = rest.indexOf(this.from)) {
      next = this.emit(rest.slice(0, i), at, marks, next)
      const start = at + i
      const end = start + this.from.length
      while (next < marks.length && marks[next] > start && marks[next] < end) next++
      if ('' !== this.to) this.out.writeStr(this.to)
      rest = rest.slice(i + this.from.length)
      at = end
    }
    const keep = this.pendingLen(rest)
    const written = rest.slice(0, rest.length - keep)
    next = this.emit(written, at, marks, next)
    this.pending = rest.slice(rest.length - keep)
    // The marks still waiting lie inside the carry, and move with it.
    const base = at + written.length
    this.marks = marks.slice(next).map((m) => m - base)
  }

  writeStr(s: string): void {
    if ('' === this.from) {
      this.out.writeStr(s)
      return
    }
    const marks = this.marks
    this.marks = []
    if ('' === this.pending) {
      this.scan(s, marks)
    } else {
      const text = this.pending + s
      this.pending = ''
      this.scan(text, marks)
    }
  }

  flush(): void {
    if ('' !== this.pending) {
      const pending = this.pending
      const marks = this.marks
      this.pending = ''
      this.marks = []
      this.emit(pending, 0, marks, 0)
    }
    this.out.flush()
  }

  // The carry has not gone anywhere; only the output beneath knows.
  hasCommitted(): boolean {
    return hasCommitted(this.out)
  }

  // Passed on at once when nothing is carried; otherwise it waits at the
  // carry's end, beside any that wait already, for the carried text to be
  // written.
  recordEnd(): void {
    if ('' === this.pending) {
      this.out.recordEnd?.()
      return
    }
    const at = this.pending.length
    if (at !== this.marks[this.marks.length - 1]) this.marks.push(at)
  }
}
