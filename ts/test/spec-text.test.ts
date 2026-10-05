/* Copyright (c) 2026 tabnas, MIT License */

// test/spec/text.tsv: the text algebra as data. A row names a stack of text
// stages over a coalescing writer and a script of operations on the
// outermost stage; the expected value is the JSON array of the chunks the
// writer received, one per `write` call, or the code of the first failing
// operation. The encodings are docs/reference.md's "Shared fixtures".

import { Limits } from '@tabnas/alchemy/shared'

import { Concat, DEFAULT_BUDGET, Join, ReplaceText, TextOut, WriteOut, Writer } from '../dist/render'

import { Row, fields, jsonColumn, malformed, runFixture } from './common'

// A writer that keeps each `write` call as one chunk and takes it whole. A
// chunk that is not UTF-8 is a failure of the writer, as in the Rust
// runner: a stage that split a character would show here.
class Recorder implements Writer {
  readonly chunks: string[] = []
  private decoder = new TextDecoder('utf-8', { fatal: true })

  write(bytes: Uint8Array): number {
    this.chunks.push(this.decoder.decode(bytes))
    return bytes.length
  }
}

function text(row: Row, json: unknown): string {
  if ('string' !== typeof json) return malformed(row, `${JSON.stringify(json)} is not a string`)
  return json
}

function count(row: Row, json: unknown, what: string): number {
  if (!Number.isInteger(json) || (json as number) < 0) return malformed(row, `${what} ${JSON.stringify(json)}`)
  return json as number
}

// Build the stack: the writer, then the stages from the innermost (the
// last listed) outwards.
function build(row: Row, recorder: Recorder): TextOut {
  const pipeline = fields(row, jsonColumn(row, 'pipeline', '{}'), ['budget', 'limit', 'stages'])
  const writer = new WriteOut(recorder).withBudget(
    undefined === pipeline.budget ? DEFAULT_BUDGET : count(row, pipeline.budget, 'budget'),
  )
  if (undefined !== pipeline.limit) {
    writer.withLimits(Limits.with({ max_output_bytes: count(row, pipeline.limit, 'limit') }))
  }
  let top: TextOut = writer
  const stages = pipeline.stages ?? []
  if (!Array.isArray(stages)) return malformed(row, `stages ${JSON.stringify(stages)}`)
  for (const stage of [...stages].reverse()) {
    const keys = null != stage && 'object' === typeof stage ? Object.keys(stage) : []
    if (1 === keys.length && 'join' === keys[0]) top = new Join(top, text(row, stage.join))
    else if (1 === keys.length && 'concat' === keys[0] && true === stage.concat) top = new Concat(top)
    else if (1 === keys.length && 'replace' === keys[0] && Array.isArray(stage.replace) && 2 === stage.replace.length) {
      top = new ReplaceText(top, text(row, stage.replace[0]), text(row, stage.replace[1]))
    } else malformed(row, `${JSON.stringify(stage)} is not a stage`)
  }
  return top
}

function item(row: Row, top: TextOut, start: boolean): void {
  if (top instanceof Join || top instanceof Concat) {
    if (start) top.itemStart()
    else top.itemEnd()
    return
  }
  malformed(row, 'item markers need a join or concat outermost')
}

function run(row: Row): string[] {
  const recorder = new Recorder()
  const top = build(row, recorder)
  const ops = jsonColumn(row, 'ops', '')
  if (!Array.isArray(ops)) return malformed(row, 'ops is a JSON array')
  for (const op of ops) {
    if ('string' === typeof op) {
      top.writeStr(op)
      continue
    }
    const keys = null != op && 'object' === typeof op ? Object.keys(op) : []
    if (1 !== keys.length || 'op' !== keys[0]) malformed(row, `${JSON.stringify(op)} is not an operation`)
    if ('start' === op.op) item(row, top, true)
    else if ('end' === op.op) item(row, top, false)
    else if ('flush' === op.op) top.flush()
    else malformed(row, `${JSON.stringify(op)} is not an operation`)
  }
  // Nothing flushed for the row: what is still buffered never reached the
  // writer, and is not in the result.
  return recorder.chunks
}

runFixture('text.tsv', 'ops', run)
