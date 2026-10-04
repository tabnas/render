/* Copyright (c) 2026 tabnas, MIT License */

// The CSV renderer beside the fixtures, ported from rs/src/csv.rs: every
// RFC 4180 Appendix A case asserts exact bytes, and what a row cannot
// record (committed output, the text a failed row leaves, the renderer's
// state after a failure, a failed flush) is asserted here.

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Cell, Fail, PublicColumn, TableEvent } from '@tabnas/alchemy/shared'

import { BytesWriter, CsvOptions, CsvRenderer, MissingText, StringOut, WriteOut } from '../dist/render'

function cols(labels: string[]): PublicColumn[] {
  return labels.map((label) => ({ label }))
}

const s = Cell.string

function num(lexeme: string): Cell {
  const v = Number(lexeme)
  return Cell.number(Number.isNaN(v) ? 0 : v, lexeme)
}

const schema = (labels: string[]): TableEvent => ({ type: 'schema', columns: cols(labels) })
const row = (cells: Cell[]): TableEvent => ({ type: 'row', cells })
const END: TableEvent = { type: 'end' }

// Render a whole table and give the text back.
function render(options: Partial<CsvOptions>, labels: string[], rows: Cell[][]): string {
  const r = new CsvRenderer(new StringOut(), options)
  r.tableEvent(schema(labels))
  for (const cells of rows) r.tableEvent(row(cells))
  r.tableEvent(END)
  return r.intoInner().intoString()
}

function standard(labels: string[], rows: Cell[][]): string {
  return render({}, labels, rows)
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

describe('CSV: RFC 4180 Appendix A, the standard profile', () => {
  it('every field is quoted and every record ends with CRLF', () => {
    assert.equal(standard(['name', 'age'], [[s('ada'), num('36')]]), '"name","age"\r\n"ada","36"\r\n')
  })

  it('empty fields are an empty quote pair', () => {
    assert.equal(standard(['a', 'b'], [[s(''), s('')]]), '"a","b"\r\n"",""\r\n')
  })

  it('commas in a field are kept inside the quotes', () => {
    assert.equal(standard(['a'], [[s('x, y, z')]]), '"a"\r\n"x, y, z"\r\n')
  })

  it('quotes in a field are doubled', () => {
    assert.equal(
      standard(['a'], [[s('say "hi"')], [s('"')], [s('""')]]),
      '"a"\r\n"say ""hi"""\r\n""""\r\n""""""\r\n',
    )
  })

  it('CR and LF inside a field are written as they are', () => {
    assert.equal(standard(['a'], [[s('line1\r\nline2\nline3\rend')]]), '"a"\r\n"line1\r\nline2\nline3\rend"\r\n')
  })

  it('unicode passes through unchanged', () => {
    assert.equal(standard(['名'], [[s('héllo 日本語 🚀')]]), '"名"\r\n"héllo 日本語 🚀"\r\n')
  })

  it('booleans write true and false', () => {
    assert.equal(standard(['a', 'b'], [[Cell.bool(false), Cell.bool(true)]]), '"a","b"\r\n"false","true"\r\n')
  })

  it('zero and other values without a lexeme take the shortest form', () => {
    const cells = [0, 50.25, 1e20, 1e21, 1e-300].map((v) => Cell.number(v))
    assert.equal(
      standard(['z', 'b', 'big', 'bigger', 'tiny'], [cells]),
      '"z","b","big","bigger","tiny"\r\n"0","50.25","100000000000000000000","1e21","1e-300"\r\n',
    )
  })

  it('big lexemes are written verbatim', () => {
    const cells = [
      num('123456789012345678901234567890'),
      num('0.1000000000000000055511151231257827'),
      num('-1.5E+308'),
      num('50.250'),
    ]
    assert.equal(
      standard(['a', 'b', 'c', 'd'], [cells]),
      '"a","b","c","d"\r\n"123456789012345678901234567890","0.1000000000000000055511151231257827","-1.5E+308","50.250"\r\n',
    )
  })

  it('duplicate labels are allowed', () => {
    assert.equal(standard(['a', 'a'], [[s('1'), s('2')]]), '"a","a"\r\n"1","2"\r\n')
  })

  it('an empty row sequence still writes the header', () => {
    assert.equal(standard(['a', 'b'], []), '"a","b"\r\n')
  })

  it('the final record ends with the newline too', () => {
    const out = standard(['a'], [[s('1')], [s('2')]])
    assert.ok(out.endsWith('"2"\r\n'))
    assert.equal(out.split('\r\n').length - 1, 3)
  })
})

describe('CSV: numbers, null and missing', () => {
  it('a lexeme that is not a JSON number is INVALID_NUMBER', () => {
    for (const bad of ['1.', '01', 'NaN', '0x10', '1_000', '']) {
      const err = fails('INVALID_NUMBER', () => render({}, ['a'], [[num(bad)]]))
      assert.equal(err.committedOutput, true, 'the header was already written')
    }
  })

  it('NaN and infinity are unrepresentable with or without a lexeme', () => {
    for (const v of [NaN, Infinity, -Infinity]) {
      fails('TARGET_VALUE_UNREPRESENTABLE', () => render({}, ['a'], [[Cell.number(v)]]))
    }
    // The lexeme spells a number, but the value beside it overflowed: the
    // row is refused whole and nothing of it is written.
    const r = new CsvRenderer(new StringOut())
    r.tableEvent(schema(['a', 'b']))
    const err = fails('TARGET_VALUE_UNREPRESENTABLE', () =>
      r.tableEvent(row([s('x'), Cell.number(Infinity, '1e999')])),
    )
    assert.equal(err.path, 'column "b", row 1')
    assert.equal(r.intoInner().asStr(), '"a","b"\r\n')
  })

  it('null writes the null text, empty by default', () => {
    assert.equal(standard(['a', 'b'], [[Cell.null, s('x')]]), '"a","b"\r\n"","x"\r\n')
    assert.equal(render({ nullText: 'NULL' }, ['a'], [[Cell.null]]), '"a"\r\n"NULL"\r\n')
  })

  it('missing is an error unless a text is configured', () => {
    const err = fails('MISSING_VALUE', () => render({}, ['a', 'b'], [[s('x'), Cell.missing]]))
    assert.match(err.message, /"b"/)
    assert.equal(err.committedOutput, true)
    assert.equal(
      render({ missing: MissingText.text('N/A') }, ['a', 'b'], [[Cell.missing, s('x')]]),
      '"a","b"\r\n"N/A","x"\r\n',
    )
    assert.equal(render({ missing: MissingText.text('') }, ['a'], [[Cell.missing]]), '"a"\r\n""\r\n')
  })

  it('a row that fails writes nothing, so records stay whole', () => {
    const r = new CsvRenderer(new StringOut())
    r.tableEvent(schema(['a', 'b']))
    const failing: [Cell[], string][] = [
      [[s('x'), Cell.missing], 'MISSING_VALUE'],
      [[s('x'), num('1.')], 'INVALID_NUMBER'],
      [[s('x'), Cell.number(NaN)], 'TARGET_VALUE_UNREPRESENTABLE'],
    ]
    for (const [cells, code] of failing) {
      fails(code, () => r.tableEvent(row(cells)))
      assert.equal(r.intoInner().asStr(), '"a","b"\r\n', code)
    }
    r.tableEvent(row([s('y'), s('z')]))
    r.tableEvent(END)
    assert.equal(r.rows(), 1)
    assert.equal(r.intoInner().asStr(), '"a","b"\r\n"y","z"\r\n')
  })
})

describe('CSV: dialects', () => {
  it('the header can be turned off', () => {
    assert.equal(render({ header: false }, ['a'], [[s('x')]]), '"x"\r\n')
    assert.equal(render({ header: false }, ['a'], []), '')
  })

  it('LF is a dialect', () => {
    assert.equal(render({ newline: 'lf' }, ['a'], [[s('x')]]), '"a"\n"x"\n')
  })

  it('a tab delimiter is a dialect', () => {
    assert.equal(render({ delimiter: '\t' }, ['a', 'b'], [[s('x,y'), s('z')]]), '"a"\t"b"\r\n"x,y"\t"z"\r\n')
  })

  it('minimal quoting quotes only what needs it', () => {
    const cells = [s('plain'), s(''), s('a,b'), s('say "hi"'), s('x\ny'), s('x\ry'), num('1.50'), Cell.null]
    assert.equal(
      render({ quoting: 'minimal' }, ['p', 'e', 'c', 'q', 'lf', 'cr', 'n', 'nul'], [cells]),
      'p,e,c,q,lf,cr,n,nul\r\nplain,,"a,b","say ""hi""","x\ny","x\ry",1.50,\r\n',
    )
  })

  it("minimal quoting quotes a field holding the dialect's delimiter", () => {
    assert.equal(
      render({ quoting: 'minimal', delimiter: ';' }, ['a', 'b'], [[s('x;y'), s('x,y')]]),
      'a;b\r\n"x;y";x,y\r\n',
    )
  })

  it('a delimiter beyond the BMP is one character', () => {
    assert.equal(render({ delimiter: '🚀' }, ['a', 'b'], [[s('x'), s('y')]]), '"a"🚀"b"\r\n"x"🚀"y"\r\n')
  })

  it('the quote, a line break and NUL cannot be the delimiter', () => {
    for (const bad of ['"', '\r', '\n', '\0']) {
      fails('TARGET_VALUE_UNREPRESENTABLE', () => new CsvRenderer(new StringOut(), { delimiter: bad }))
    }
    for (const ok of [',', ';', '\t', '|', ' ', 'x', '→']) {
      assert.doesNotThrow(() => new CsvRenderer(new StringOut(), { delimiter: ok }), ok)
    }
  })

  it('a delimiter is one character, not none or several', () => {
    // Rust's `char` makes this a type error; a string must be checked.
    for (const bad of ['', ',,', '\r\n']) {
      fails('TARGET_VALUE_UNREPRESENTABLE', () => new CsvRenderer(new StringOut(), { delimiter: bad }))
    }
  })

  it('the defaults are the standard profile', () => {
    assert.deepEqual(CsvOptions.default(), {
      delimiter: ',',
      newline: 'crlf',
      header: true,
      nullText: '',
      missing: MissingText.error,
      quoting: 'always',
    })
  })
})

describe('CSV: the protocol', () => {
  it('zero columns is unrepresentable', () => {
    const err = fails('TARGET_VALUE_UNREPRESENTABLE', () => render({}, [], []))
    assert.equal(err.committedOutput, false)
  })

  it('a row before the schema is a protocol error', () => {
    const r = new CsvRenderer(new StringOut())
    const err = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([s('x')])))
    assert.equal(err.committedOutput, false)
  })

  it('a second schema is a protocol error', () => {
    const r = new CsvRenderer(new StringOut())
    r.tableEvent(schema(['a']))
    const err = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(schema(['a'])))
    assert.equal(err.committedOutput, true)
  })

  it('a row of the wrong width is a protocol error', () => {
    for (const cells of [[], [s('1')], [s('1'), s('2'), s('3')]]) {
      const err = fails('PROTOCOL_ORDER_ERROR', () => render({}, ['a', 'b'], [cells]))
      assert.match(err.message, /row 1 has/)
    }
  })

  it('an end before the schema is a protocol error', () => {
    const r = new CsvRenderer(new StringOut())
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(END))
  })

  it('events after the end are protocol errors', () => {
    const r = new CsvRenderer(new StringOut())
    r.tableEvent(schema(['a']))
    r.tableEvent(END)
    assert.equal(r.isDone(), true)
    for (const ev of [END, row([s('x')]), schema(['a'])]) {
      const err = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(ev))
      assert.equal(err.committedOutput, true)
    }
  })

  it('an event or a cell the protocol does not define is a protocol error', () => {
    const r = new CsvRenderer(new StringOut())
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent({ type: 'rows' } as any))
    r.tableEvent(schema(['a']))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([{ type: 'date' } as any])))
    fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(row([{ type: 'string', value: 1 } as any])))
    assert.equal(r.intoInner().asStr(), '"a"\r\n')
  })

  it('a failed flush at the end leaves the renderer not done', () => {
    const out = new WriteOut({
      write: (b: Uint8Array) => b.length,
      flush: () => {
        throw new Error('pipe closed')
      },
    })
    const r = new CsvRenderer(out)
    r.tableEvent(schema(['a']))
    fails('OUTPUT_FAILED', () => r.tableEvent(END))
    assert.equal(r.isDone(), false)
  })

  it('committed output means bytes that reached the writer', () => {
    // Buffered in the WriteOut, not yet written: the failure leaves no
    // partial output behind, and says so.
    const r = new CsvRenderer(new WriteOut(new BytesWriter()))
    r.tableEvent(schema(['a']))
    const err = fails('PROTOCOL_ORDER_ERROR', () => r.tableEvent(schema(['a'])))
    assert.equal(err.committedOutput, false)
    assert.equal(r.intoInner().committed(), 0)
    assert.equal(r.intoInner().intoInner().text(), '')

    // Written through: partial output exists.
    const r2 = new CsvRenderer(new WriteOut(new BytesWriter()).withBudget(0))
    r2.tableEvent(schema(['a']))
    const err2 = fails('PROTOCOL_ORDER_ERROR', () => r2.tableEvent(schema(['a'])))
    assert.equal(err2.committedOutput, true)
    assert.equal(r2.intoInner().intoInner().text(), '"a"\r\n')
  })

  it('the end flushes the output and nothing else does', () => {
    const r = new CsvRenderer(new WriteOut(new BytesWriter()))
    r.tableEvent(schema(['a']))
    r.tableEvent(row([s('x')]))
    assert.equal(r.intoInner().committed(), 0)
    assert.equal(r.tableEvent(END), 'continue')
    assert.equal(r.rows(), 1)
    assert.equal(r.intoInner().committed(), 10)
    assert.equal(r.intoInner().intoInner().text(), '"a"\r\n"x"\r\n')
  })
})
