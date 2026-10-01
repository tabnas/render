/* Copyright (c) 2026 tabnas, MIT License */

// Number text beside the fixtures, ported from rs/src/number.rs: the JSON
// number grammar, the check, and the layout of a lexeme-less value, which
// is no runtime's default and is pinned against `String()` here too, over
// many doubles, so the digits are JavaScript's own and only the layout is
// ours.

import { describe, it } from 'node:test'
import assert from 'node:assert'

import { Fail } from '@tabnas/transduce'

import { checkNumber, isJsonNumber, writeValue } from '../dist/render'

function code(fn: () => void): string | undefined {
  try {
    fn()
  } catch (err) {
    assert.ok(err instanceof Fail)
    return err.code
  }
  return undefined
}

describe('number', () => {
  it('the JSON number grammar is exact', () => {
    for (const ok of [
      '0',
      '-0',
      '1',
      '-1',
      '10',
      '1.5',
      '0.0',
      '1e5',
      '1E5',
      '1e+5',
      '1e-5',
      '1.5e10',
      '123456789012345678901234567890',
      '-0.000001',
    ]) {
      assert.ok(isJsonNumber(ok), `${JSON.stringify(ok)} should be a number`)
    }
    for (const bad of [
      '',
      '-',
      '+1',
      '01',
      '1.',
      '.5',
      '1e',
      '1e+',
      '1.e5',
      '0x10',
      'NaN',
      'Infinity',
      '-Infinity',
      ' 1',
      '1 ',
      '1\n',
      '1_000',
      '1,5',
      '١',
      '１',
    ]) {
      assert.ok(!isJsonNumber(bad), `${JSON.stringify(bad)} should not be a number`)
    }
  })

  it('a JSON number lexeme beside a finite value passes', () => {
    assert.equal(code(() => checkNumber(1, '1.00')), undefined)
    assert.equal(code(() => checkNumber(1, null)), undefined)
    assert.equal(code(() => checkNumber(1e30, '123456789012345678901234567890')), undefined)
  })

  it('a lexeme that is not a JSON number is INVALID_NUMBER, judged before the value', () => {
    assert.equal(code(() => checkNumber(1, '1.')), 'INVALID_NUMBER')
    assert.equal(code(() => checkNumber(NaN, 'NaN')), 'INVALID_NUMBER')
  })

  it('a non-finite value is unrepresentable with or without a lexeme', () => {
    for (const v of [NaN, Infinity, -Infinity]) {
      assert.equal(code(() => checkNumber(v, null)), 'TARGET_VALUE_UNREPRESENTABLE')
    }
    try {
      checkNumber(Infinity, '1e999')
      assert.fail('should throw')
    } catch (err: any) {
      assert.equal(err.code, 'TARGET_VALUE_UNREPRESENTABLE')
      assert.match(err.message, /"1e999"/)
    }
    assert.equal(code(() => checkNumber(-Infinity, '-1e999')), 'TARGET_VALUE_UNREPRESENTABLE')
  })

  it('a value takes the shortest form, positional within the JavaScript range', () => {
    assert.equal(writeValue(1), '1')
    assert.equal(writeValue(0.1), '0.1')
    assert.equal(writeValue(-0), '-0')
    assert.equal(writeValue(0), '0')
    assert.equal(writeValue(50.25), '50.25')
    assert.equal(writeValue(123456.789), '123456.789')
    assert.equal(writeValue(1e20), '100000000000000000000')
    assert.equal(writeValue(1.5e17), '150000000000000000')
    assert.equal(writeValue(1.23456789e18), '1234567890000000000')
    assert.equal(writeValue(0.000001), '0.000001')
    assert.equal(writeValue(-0.000025), '-0.000025')
  })

  it('a value outside the positional range takes the exponent form', () => {
    assert.equal(writeValue(1e21), '1e21')
    assert.equal(writeValue(1e300), '1e300')
    assert.equal(writeValue(1e-300), '1e-300')
    assert.equal(writeValue(1e-7), '1e-7')
    assert.equal(writeValue(-2.5e-8), '-2.5e-8')
    assert.equal(writeValue(5e-324), '5e-324')
    assert.equal(writeValue(Number.MAX_VALUE), '1.7976931348623157e308')
    assert.equal(writeValue(-1.5e300), '-1.5e300')
  })

  it('every form is a JSON number that reads back as the same value', () => {
    const values = [
      0,
      -0,
      1,
      0.1,
      1e20,
      1e21,
      1e300,
      1e-300,
      1e-6,
      1e-7,
      5e-324,
      Number.MAX_VALUE,
      -Number.MAX_VALUE,
      2.2250738585072014e-308,
      123456.789,
      2 ** 53,
      9.999999999999999e20,
    ]
    for (const v of values) {
      const t = writeValue(v)
      assert.ok(isJsonNumber(t), `${v} wrote ${t}`)
      assert.ok(Object.is(Number(t), v), `${v} wrote ${t}`)
    }
  })

  it("the reviewer's probe is bytes, not hundreds of digits", () => {
    const total = [1e300, 1e-300, 1.5e17, 1.23456789e18].map((v) => writeValue(v).length).reduce((a, b) => a + b)
    assert.equal(total, 5 + 6 + 18 + 19)
  })

  it("has String()'s digits and the fixtures' layout, over many doubles", () => {
    // String() is JavaScript's shortest round-trip text, positional in
    // exactly [1e-6, 1e21); the only differences the layout allows are the
    // exponent's `+` and negative zero.
    const view = new DataView(new ArrayBuffer(8))
    let seed = 0x2545f491
    const next = (): number => {
      seed ^= seed << 13
      seed ^= seed >>> 17
      seed ^= seed << 5
      return seed >>> 0
    }
    let checked = 0
    for (let i = 0; i < 200_000; i++) {
      view.setUint32(0, next())
      view.setUint32(4, next())
      const v = view.getFloat64(0)
      if (!Number.isFinite(v)) continue
      const t = writeValue(v)
      const want = Object.is(v, -0) ? '-0' : String(v).replace('e+', 'e')
      if (t !== want) assert.equal(t, want, `bits ${view.getBigUint64(0).toString(16)}`)
      if (!Object.is(Number(t), v)) assert.fail(`${t} does not read back`)
      checked++
    }
    assert.ok(checked > 190_000)
    // Random bits are mostly far from 1; these are around the thresholds.
    for (let i = 0; i < 200_000; i++) {
      const v = ((next() / 2 ** 32) * 10 ** ((next() % 40) - 20)) * (1 & next() ? -1 : 1)
      const t = writeValue(v)
      const want = Object.is(v, -0) ? '-0' : String(v).replace('e+', 'e')
      if (t !== want) assert.equal(t, want, String(v))
    }
  })
})
