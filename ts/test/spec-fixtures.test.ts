/* Copyright (c) 2026 tabnas, MIT License */

// Every fixture in ../../test/spec has a runner in this directory; a new
// file added without one fails here rather than passing silently.

import { describe, it } from 'node:test'
import assert from 'node:assert'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { FIXTURES, specDir } from './common'

describe('spec fixtures', () => {
  it('every fixture has a runner', () => {
    const files = readdirSync(specDir())
      .filter((f) => f.endsWith('.tsv'))
      .sort()
    assert.deepEqual(files, [...FIXTURES].sort())
    for (const file of FIXTURES) {
      const base = file.replace(/\.tsv$/, '')
      const source = readFileSync(join(__dirname, '..', 'test', `spec-${base}.test.ts`), 'utf8')
      assert.ok(source.includes(`runFixture('${file}'`) || source.includes(`'${file}',`), `the runner for ${file} reads it`)
    }
  })
})
