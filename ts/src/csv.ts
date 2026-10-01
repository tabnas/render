/* Copyright (c) 2026 tabnas, MIT License */

// `TableRows/1` as CSV: the always-quoted profile.
//
// The standard profile quotes every field, doubles `"`, ends every record
// with CRLF and writes numbers as their lexemes. Always quoting is a
// decision, not a habit: it makes the output independent of the data (no
// field can change the record's shape), it makes the empty string and the
// null text distinguishable from a missing quote pair, and it lets a
// reader tell that a field was a field. Minimal quoting and other
// delimiters are dialects the caller selects explicitly, and they are valid
// here because a row is a finite vector: the renderer sees the whole field
// before it decides how to write it.
//
// The renderer validates the protocol as it goes, because a third-party
// transducer or a host adapter is as much a source of `TableRows/1` as the
// standard table transducer is.

import { Cell, Fail, Flow, PublicColumn, TableEvent, TableSink } from '@tabnas/transduce'

import { checkNumber, writeValue } from './number'
import { TextOut, hasCommitted } from './text'

// The record terminator: `crlf`, RFC 4180's and the standard profile's, or
// `lf`.
export type Newline = 'crlf' | 'lf'

export const Newline = Object.freeze({
  CRLF: 'crlf' as Newline,
  LF: 'lf' as Newline,
  // The terminator's text.
  text(newline: Newline): string {
    return 'lf' === newline ? '\n' : '\r\n'
  },
})

// When a field is quoted: `always`, the standard profile, or `minimal`,
// only a field holding the delimiter, `"`, CR or LF. Under `minimal` an
// empty field is written as nothing, so the empty string and an empty null
// text read back the same; that is the dialect's trade-off, not a defect.
export type Quoting = 'always' | 'minimal'

// What a `missing` cell becomes: an `error` (`MISSING_VALUE`: a table that
// promised a column and did not deliver it is not silently padded), or
// this `text` instead.
export type MissingText = { readonly type: 'error' } | { readonly type: 'text'; readonly text: string }

export const MissingText = Object.freeze({
  error: Object.freeze({ type: 'error' }) as MissingText,
  text(text: string): MissingText {
    return Object.freeze({ type: 'text', text })
  },
})

// The CSV dialect.
export type CsvOptions = {
  // One character, and not `"`, CR, LF or NUL: those would make the output
  // unreadable by construction, and are refused when the renderer is
  // built.
  delimiter: string
  newline: Newline
  // Write the labels as the first record.
  header: boolean
  // The text of a `null` cell; empty by default.
  nullText: string
  missing: MissingText
  quoting: Quoting
}

export const CsvOptions = Object.freeze({
  // The standard profile: `,`, CRLF, a header, `null` as the empty text,
  // `missing` an error, every field quoted.
  default(): CsvOptions {
    return {
      delimiter: ',',
      newline: 'crlf',
      header: true,
      nullText: '',
      missing: MissingText.error,
      quoting: 'always',
    }
  },
})

type Phase = 'before_schema' | 'rows' | 'done'

// Renders `TableRows/1` as CSV.
//
// One schema first, rows exactly as wide as the schema, one end: anything
// else is `PROTOCOL_ORDER_ERROR`. A schema with no columns has no CSV form
// (a record cannot be empty) and is `TARGET_VALUE_UNREPRESENTABLE`. Every
// record, the header included, ends with the configured newline, the last
// one too. The output is flushed once, at the end, so a table that fails
// half way is not flushed as if it were whole; a failure found after any
// text was written says so with `committedOutput`.
export class CsvRenderer<O extends TextOut = TextOut> implements TableSink {
  private out: O
  private opts: CsvOptions
  private newline: string
  private phase: Phase = 'before_schema'
  private labels: string[] = []
  private count = 0
  private emitted = false

  // A renderer over `out`, with `options` over the standard profile.
  // Throws `TARGET_VALUE_UNREPRESENTABLE` when the delimiter is one no CSV
  // reader could take, or is not one character.
  constructor(out: O, options?: Partial<CsvOptions>) {
    const opts: CsvOptions = { ...CsvOptions.default(), ...(options ?? {}) }
    const d = opts.delimiter
    if ('string' !== typeof d || 1 !== [...d].length) {
      throw new Fail(
        'TARGET_VALUE_UNREPRESENTABLE',
        `${JSON.stringify(d)} cannot be a CSV delimiter: a delimiter is one character`,
      )
    }
    if ('"' === d || '\r' === d || '\n' === d || '\0' === d) {
      throw new Fail(
        'TARGET_VALUE_UNREPRESENTABLE',
        `${JSON.stringify(d)} cannot be a CSV delimiter: it is the quote, a line break or NUL`,
      )
    }
    if ('crlf' !== opts.newline && 'lf' !== opts.newline) {
      throw new TypeError(`not a newline: ${JSON.stringify(opts.newline)}`)
    }
    if ('always' !== opts.quoting && 'minimal' !== opts.quoting) {
      throw new TypeError(`not a quoting: ${JSON.stringify(opts.quoting)}`)
    }
    this.out = out
    this.opts = Object.freeze(opts)
    this.newline = Newline.text(opts.newline)
  }

  options(): Readonly<CsvOptions> {
    return this.opts
  }

  // Rows written so far.
  rows(): number {
    return this.count
  }

  // Whether the end has been rendered.
  isDone(): boolean {
    return 'done' === this.phase
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

  tableEvent(ev: TableEvent): Flow {
    switch (ev.type) {
      case 'schema':
        this.schema(ev.columns)
        break
      case 'row':
        this.row(ev.cells)
        break
      case 'end':
        this.end()
        break
      default:
        throw this.fail(Fail.protocol(`not a table event: ${JSON.stringify((ev as any)?.type)}`))
    }
    return 'continue'
  }

  private schema(columns: readonly PublicColumn[]): void {
    if ('rows' === this.phase) throw this.fail(Fail.protocol('a second schema'))
    if ('done' === this.phase) throw this.fail(Fail.protocol('a schema after the end'))
    if (0 === columns.length) {
      throw new Fail('TARGET_VALUE_UNREPRESENTABLE', 'a table with no columns has no CSV form')
    }
    this.labels = columns.map((c) => c.label)
    this.phase = 'rows'
    if (this.opts.header) {
      this.emitted = true
      this.labels.forEach((label, i) => {
        if (0 < i) this.out.writeStr(this.opts.delimiter)
        this.field(label)
      })
      this.out.writeStr(this.newline)
    }
  }

  private row(cells: readonly Cell[]): void {
    if ('before_schema' === this.phase) throw Fail.protocol('a row before the schema')
    if ('done' === this.phase) throw this.fail(Fail.protocol('a row after the end'))
    if (cells.length !== this.labels.length) {
      throw this.fail(
        Fail.protocol(
          `row ${this.count + 1} has ${cells.length} cells; the schema has ${this.labels.length} columns`,
        ),
      )
    }
    try {
      this.check(cells)
    } catch (err) {
      throw err instanceof Fail ? this.fail(err) : err
    }
    for (let i = 0; i < cells.length; i++) {
      if (0 < i) this.out.writeStr(this.opts.delimiter)
      const cell = cells[i]
      let text: string
      switch (cell.type) {
        case 'null':
          text = this.opts.nullText
          break
        case 'bool':
          text = cell.value ? 'true' : 'false'
          break
        // `check` passed the row: the lexeme is a JSON number and the value
        // is finite, so this pass only formats, once.
        case 'number':
          text = null != cell.lexeme ? cell.lexeme : writeValue(cell.value)
          break
        case 'string':
          text = cell.value
          break
        default:
          // `missing`: `check` rejected the row already unless a text is
          // configured.
          if ('text' !== this.opts.missing.type) continue
          text = this.opts.missing.text
      }
      this.emitted = true
      this.field(text)
    }
    this.emitted = true
    this.out.writeStr(this.newline)
    this.count++
  }

  // Reject a row before any of it is written, so a row is rendered whole or
  // not at all and the output stays a sequence of complete records whatever
  // the caller does after a failure. Nothing is formatted here; `row`
  // formats each number once, after the row has passed.
  private check(cells: readonly Cell[]): void {
    for (let i = 0; i < cells.length; i++) {
      const cell = cells[i]
      switch (cell?.type) {
        case 'number':
          try {
            checkNumber(cell.value, cell.lexeme)
          } catch (err) {
            if (err instanceof Fail) {
              err.atPath(`column ${JSON.stringify(this.labels[i])}, row ${this.count + 1}`)
            }
            throw err
          }
          break
        case 'missing':
          if ('error' === this.opts.missing.type) {
            throw new Fail(
              'MISSING_VALUE',
              `row ${this.count + 1} has no value for column ${JSON.stringify(this.labels[i])}`,
            )
          }
          break
        case 'null':
          break
        case 'bool':
          if ('boolean' !== typeof cell.value) throw this.notACell(i)
          break
        case 'string':
          if ('string' !== typeof cell.value) throw this.notACell(i)
          break
        default:
          throw this.notACell(i)
      }
    }
  }

  // A cell no `Cell` constructor makes: a third-party source's defect, and
  // a protocol error like any other.
  private notACell(i: number): Fail {
    return Fail.protocol(
      `row ${this.count + 1} has no cell this protocol defines in column ${JSON.stringify(this.labels[i])}`,
    )
  }

  private end(): void {
    if ('before_schema' === this.phase) throw Fail.protocol('the end before the schema')
    if ('done' === this.phase) throw this.fail(Fail.protocol('a second end'))
    // Done only once the flush has succeeded: a table whose last bytes
    // never reached the writer is not done, whatever the end said.
    this.out.flush()
    this.phase = 'done'
  }

  // Write one field: quoted with `"` doubled, or bare when the dialect
  // allows and the text needs no quoting.
  private field(text: string): void {
    const out = this.out
    if (
      'minimal' === this.opts.quoting &&
      !text.includes(this.opts.delimiter) &&
      !text.includes('"') &&
      !text.includes('\r') &&
      !text.includes('\n')
    ) {
      out.writeStr(text)
      return
    }
    out.writeStr('"')
    let from = 0
    for (let i = text.indexOf('"'); -1 !== i; i = text.indexOf('"', from)) {
      // Up to and including the quote, then the quote again: doubled.
      out.writeStr(text.slice(from, i + 1))
      out.writeStr('"')
      from = i + 1
    }
    if (from < text.length) out.writeStr(from === 0 ? text : text.slice(from))
    out.writeStr('"')
  }
}
