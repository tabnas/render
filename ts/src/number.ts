/* Copyright (c) 2026 tabnas, MIT License */

// Number text, shared by the renderers.
//
// A number reaches a renderer as a machine value and, when the source
// could hand it over, the lexeme it was spelled with. The lexeme wins,
// because it is the only thing that keeps `50.25` as `50.25` and keeps the
// digits of a number beyond a double's exact range; but a lexeme is data
// from a source, so it is checked against the JSON number grammar before
// it is copied into an output that promises to be JSON or CSV. Without a
// lexeme the shortest text that reads back as the same double is written.
// A value with no finite text (NaN, infinity) is rejected as
// unrepresentable rather than written as `null`, which would silently
// change the data, and it is rejected whatever lexeme stands beside it:
// `1e999` spells a number, but the value the pipeline holds is infinity,
// and a JSON reader given the lexeme refuses it as out of range.
//
// Validation and formatting are separate functions so a renderer can check
// a whole row before it writes any of it, and format each number once.

import { Fail } from '@tabnas/alchemy/shared'

function isDigit(c: number): boolean {
  return c >= 0x30 && c <= 0x39
}

// Whether `text` is a number by RFC 8259's grammar:
// `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`, nothing else and
// nothing around it.
export function isJsonNumber(text: string): boolean {
  const n = text.length
  let i = 0
  if (0x2d === text.charCodeAt(i)) i++
  const lead = text.charCodeAt(i)
  if (0x30 === lead) {
    i++
  } else if (lead >= 0x31 && lead <= 0x39) {
    i++
    while (i < n && isDigit(text.charCodeAt(i))) i++
  } else {
    return false
  }
  if (0x2e === text.charCodeAt(i)) {
    i++
    const start = i
    while (i < n && isDigit(text.charCodeAt(i))) i++
    if (i === start) return false
  }
  const e = text.charCodeAt(i)
  if (0x65 === e || 0x45 === e) {
    i++
    const sign = text.charCodeAt(i)
    if (0x2b === sign || 0x2d === sign) i++
    const start = i
    while (i < n && isDigit(text.charCodeAt(i))) i++
    if (i === start) return false
  }
  return i === n
}

// Whether a renderer may write this number at all; throws the `Fail` that
// refuses it.
//
// A lexeme that is not a JSON number is `INVALID_NUMBER`. A value that is
// not finite is `TARGET_VALUE_UNREPRESENTABLE`, with or without a lexeme:
// the design brief names NaN and infinity unrepresentable, and a lexeme
// such as `1e999` would hand the reader a number the pipeline never had.
// Nothing is formatted here, so a renderer can run this over a whole row
// before writing a byte of it.
export function checkNumber(value: number, lexeme: string | null | undefined): void {
  if (null != lexeme && !isJsonNumber(lexeme)) {
    throw new Fail('INVALID_NUMBER', `${JSON.stringify(lexeme)} is not a JSON number`)
  }
  if ('number' !== typeof value || !Number.isFinite(value)) {
    const message =
      null != lexeme
        ? `${JSON.stringify(lexeme)} is ${value} as a number, which has no representation`
        : `${value} has no representation as a number`
    throw new Fail('TARGET_VALUE_UNREPRESENTABLE', message)
  }
}

// The magnitudes written positionally: from `1e-6` up to, not including,
// `1e21`. These are the thresholds JavaScript's `Number#toString` uses, so
// they are the ones most JSON in circulation was written with; an integer
// of up to 21 digits stays an integer, and `1e300` is five characters
// rather than 301.
const POSITIONAL_MIN = 1e-6
const POSITIONAL_MAX = 1e21

// The shortest text that reads back as `value`, which must be finite
// (`checkNumber` establishes that before every call).
//
// The digits are the shortest that round-trip, as `toExponential()` with
// no argument gives them; this function chooses only the layout, and it is
// not JavaScript's: positional for zero and for magnitudes within
// [`POSITIONAL_MIN`, `POSITIONAL_MAX`), exponent form outside with no `+`
// and no leading zeros in the exponent (`1e21`, `1e-7`, where `String`
// writes `1e+21`), and negative zero as `-0` (where `String` writes `0`).
// That is the layout of test/spec/number.tsv, and the Rust crate's.
export function writeValue(value: number): string {
  if (0 === value) return Object.is(value, -0) ? '-0' : '0'
  const neg = value < 0
  const magnitude = neg ? -value : value
  // `d.ddde±x`: the shortest round-trip digits and the decimal exponent.
  const exp = magnitude.toExponential()
  const at = exp.indexOf('e')
  const digits = exp.charAt(0) + exp.slice(2, at)
  const e = parseInt(exp.slice(at + 1), 10)
  const n = digits.length
  let text: string
  if (magnitude >= POSITIONAL_MIN && magnitude < POSITIONAL_MAX) {
    if (e >= n - 1) {
      text = digits + '0'.repeat(e - (n - 1))
    } else if (e >= 0) {
      text = digits.slice(0, e + 1) + '.' + digits.slice(e + 1)
    } else {
      text = '0.' + '0'.repeat(-e - 1) + digits
    }
  } else {
    text = (1 < n ? digits.charAt(0) + '.' + digits.slice(1) : digits) + 'e' + e
  }
  return neg ? '-' + text : text
}
