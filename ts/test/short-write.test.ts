/* Copyright (c) 2026 tabnas, MIT License */

// The short write on a real file, as rs/src/text.rs tests it: under a
// file-size limit the kernel accepts the bytes up to the limit and refuses
// the rest, and the file on disk must hold exactly `committed()` bytes.
// `RLIMIT_FSIZE` is the limit that needs no privileges; it is set per
// process by the shell's `ulimit -f`, so the run happens in a child
// process, and the shell ignores `SIGXFSZ` first so that the write past the
// limit fails with `EFBIG` instead of ending the child. Where no shell can
// impose the limit, the test says so and passes.

import { describe, it } from 'node:test'
import assert from 'node:assert'
import { spawnSync } from 'node:child_process'
import { join } from 'node:path'

const CHILD = `
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const { Metrics } = require('@tabnas/transduce')
const { FdWriter, WriteOut } = require(${JSON.stringify(join(__dirname, '..', 'dist', 'render'))})
console.log('short-write: start')
const file = path.join(os.tmpdir(), 'tabnas-render-short-write-' + process.pid + '.txt')
const fd = fs.openSync(file, 'w')
const metrics = new Metrics()
const out = new WriteOut(new FdWriter(fd)).withMetrics(metrics)
const text = '0123456789abcdef'.repeat(1000)
out.writeStr(text)
if (0 !== out.committed()) throw new Error('under the default budget it is buffered')
let err = null
try { out.flush() } catch (e) { err = e }
const committed = out.committed()
const onDisk = fs.fstatSync(fd).size
fs.closeSync(fd)
fs.rmSync(file, { force: true })
if (null == err) {
  console.log('short-write: skipped, no file-size limit was in force (' + onDisk + ' bytes on disk)')
} else if (0 === committed && 0 === onDisk) {
  console.log('short-write: skipped, the limit refused the write whole: ' + err.message)
} else {
  console.log('short-write: ran ' + JSON.stringify({
    committed, onDisk, outputBytes: metrics.output_bytes, code: err.code,
    committedOutput: err.committedOutput, hasCommitted: out.hasCommitted(), length: text.length,
  }))
}
`

describe('WriteOut over a file under a size limit', () => {
  it('the file holds exactly the committed bytes', () => {
    const run = spawnSync(
      'sh',
      ['-c', 'trap "" XFSZ && ulimit -f 8 && exec "$0" "$@"', process.execPath, '-e', CHILD],
      { encoding: 'utf8', cwd: join(__dirname, '..'), timeout: 30_000 },
    )
    if (null != run.error) {
      console.log(`skipped: no sh to impose a file-size limit with (${run.error.message})`)
      return
    }
    const report = run.stdout
      .split('\n')
      .reverse()
      .find((l) => l.startsWith('short-write: '))
    if (undefined === report) {
      console.log(`skipped: the shell could not impose a file-size limit (status ${run.status}): ${run.stderr.trim()}`)
      return
    }
    assert.equal(run.status, 0, `${run.stdout}\n${run.stderr}`)
    if (report.startsWith('short-write: skipped')) {
      console.log(report)
      return
    }
    assert.ok(report.startsWith('short-write: ran '), report)
    const r = JSON.parse(report.slice('short-write: ran '.length))
    console.log(report)
    assert.equal(r.code, 'OUTPUT_FAILED')
    assert.equal(r.committedOutput, true, 'the kernel took part of the buffer, so output is partial')
    assert.equal(r.hasCommitted, true)
    assert.equal(r.committed, r.onDisk, 'committed() must be what the file holds')
    assert.equal(r.outputBytes, r.committed)
    assert.ok(r.committed < r.length, 'the limit cut the write')
  })
})
