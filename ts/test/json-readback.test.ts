/* Copyright (c) 2026 tabnas, MIT License */

// The rendered JSON, read back by an independent reader, as
// rs/tests/json_readback.rs reads it with serde_json.
//
// Every fixture in rs/tests/fixtures (copied from aless) that a grammar in
// the devDependencies reads is parsed, walked with transduce's
// `ValueSource` into the renderer, and the text is parsed again by
// `JSON.parse`. The result must equal the parsed value, member order
// included: the comparison is on `JSON.stringify` of both, since a deep
// equality would compare objects as sets.

import { describe, it } from 'node:test'
import assert from 'node:assert'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { Tabnas } from '@tabnas/parser'
import { make as makeJson } from '@tabnas/json'
import { make as makeJsonl } from '@tabnas/jsonl'
import { make as makeCsv } from '@tabnas/csv'
import { Yaml } from '@tabnas/yaml'
import { ValueSource } from '@tabnas/transduce'

import { JsonOptions, JsonRenderer, StringOut } from '../dist/render'

// The YAML grammar is a plugin over the jsonic base grammar, a declared
// dev-dependency here as it is a peer of @tabnas/yaml.
import { jsonic } from '@tabnas/jsonic'

function fixture(name: string): string {
  return readFileSync(join(__dirname, '..', '..', 'rs', 'tests', 'fixtures', name), 'utf8')
}

function render(value: unknown, options: Partial<JsonOptions>): string {
  const renderer = new JsonRenderer(new StringOut(), options)
  new ValueSource(value).run(renderer)
  assert.equal(renderer.isDone(), true)
  return renderer.intoInner().intoString()
}

function assertRoundTrip(name: string, value: unknown): void {
  const want = JSON.stringify(value)
  for (const options of [{}, { indent: 2, trailingNewline: true }]) {
    const text = render(value, options)
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch (err) {
      assert.fail(`${name} with ${JSON.stringify(options)}: ${err}\n${text}`)
    }
    assert.equal(JSON.stringify(parsed), want, `${name} with ${JSON.stringify(options)}`)
  }
  assert.ok(!render(value, {}).includes('\n'), `${name}: compact output has a line break`)
}

describe('JSON read back', () => {
  it('the JSON fixtures read back as the parsed document', () => {
    for (const name of ['sample.json', 'nested.json']) {
      assertRoundTrip(name, makeJson().parse(fixture(name)))
    }
  })

  it('the JSON Lines fixture reads back as the array of its lines', () => {
    const value = makeJsonl().parse(fixture('sample.jsonl'))
    assertRoundTrip('sample.jsonl', value)
    assert.equal((value as unknown[]).length, 3)
  })

  it('the YAML fixture reads back as the parsed document', () => {
    assertRoundTrip('sample.yaml', new Tabnas().use(jsonic).use(Yaml).parse(fixture('sample.yaml')))
  })

  it('the CSV fixture reads back as the parsed records', () => {
    assertRoundTrip('sample.csv', makeCsv().parse(fixture('sample.csv')))
  })

  it('the compact rendering of the JSON fixture is the expected bytes', () => {
    assert.equal(
      render(makeJson().parse(fixture('sample.json')), {}),
      '{"store":{"name":"corner shop","open":true,"books":[{"title":"SICP","price":42.5,"tags":["cs","classic"]},{"title":"TAPL","price":55,"tags":["types"]}],"counts":{"fiction":12,"science":7}},"version":3}',
    )
  })

  it("the TSV fixture reads back through the CSV grammar's tab dialect", () => {
    const value = makeCsv({ field: { separation: '\t' } }).parse(fixture('sample.tsv'))
    assertRoundTrip('sample.tsv', value)
    assert.equal(render(value, {}), '[{"name":"ada","age":"36"},{"name":"lin","age":"28"}]')
  })
})
