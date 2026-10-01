/* Copyright (c) 2026 tabnas, MIT License */

// `TableRows/1` as `JsonEvents/1`: an array of records keyed by label.
//
// A table has a natural JSON form, one object per row with the column
// labels as member names, and producing it as events rather than text
// means the JSON renderer, and every other `JsonEvents/1` consumer, gets it
// for free. The stage retains the labels and nothing else: each row is
// emitted as it arrives and forgotten. Labels are data from the source's
// metadata, so a repeated label is not an error here (the CSV renderer
// allows it too); it is resolved the way a JSON reader would resolve a
// repeated member in the un-deduplicated record, by keeping the last value
// that is there, and the output then carries each label once.

import { Cell, Ev, Fail, Flow, JsonEvent, PublicColumn, Sink, TableEvent, TableSink } from '@tabnas/transduce'

// What a `missing` cell becomes in a record: `skip` leaves the member out
// (the record says nothing where the source had nothing, which is what an
// absent path meant), `null` writes the member with a `null` value, and
// `error` fails the run with `MISSING_VALUE`.
export type MissingRecord = 'skip' | 'null' | 'error'

type Phase = 'before_schema' | 'rows' | 'done'

// Whether a cell contributes a member to its record under this policy:
// every cell but a `missing` that is skipped.
function contributes(cell: Cell, missing: MissingRecord): boolean {
  return !('missing' === cell.type && 'skip' === missing)
}

// Turns `TableRows/1` into `JsonEvents/1`.
//
// The schema opens the array, each row is one object whose members are
// the labels in schema order, the end closes the array and ends the
// document. The protocol is validated as the CSV renderer validates it:
// one schema first, rows of the schema's width, one end,
// `PROTOCOL_ORDER_ERROR` otherwise. A schema with no columns is allowed,
// since an empty object is a JSON value; the CSV renderer's refusal is
// about CSV. When a label repeats, each record carries it once, from the
// last column whose cell contributes a member (under `skip` a `missing`
// cell contributes none), in that column's position: the value a reader of
// the un-deduplicated record would keep, since a member that was never
// written cannot win. A failure found after events were forwarded is
// marked as having committed output, since the stage downstream may have
// rendered them. A failure the sink throws reaches the caller unchanged.
export class RecordsToJson<S extends Sink = Sink> implements TableSink {
  private sink: S
  private missing: MissingRecord = 'skip'
  private phase: Phase = 'before_schema'
  private labels: string[] = []
  // One key event per column, made once at the schema, so a row costs no
  // key allocation.
  private keys: JsonEvent[] = []
  // Per column, the next later column with the same label, so a row can
  // find the column that carries the label's value; -1 for the common
  // case of a label that does not repeat.
  private nextSame: number[] = []
  private count = 0
  private forwarded = false

  constructor(sink: S, missing?: MissingRecord) {
    this.sink = sink
    if (undefined !== missing) this.withMissing(missing)
  }

  withMissing(missing: MissingRecord): this {
    if ('skip' !== missing && 'null' !== missing && 'error' !== missing) {
      throw new TypeError(`not a missing policy: ${JSON.stringify(missing)}`)
    }
    this.missing = missing
    return this
  }

  // Rows emitted so far.
  rows(): number {
    return this.count
  }

  // Whether the end has been forwarded.
  isDone(): boolean {
    return 'done' === this.phase
  }

  intoInner(): S {
    return this.sink
  }

  private fail(f: Fail): Fail {
    return this.forwarded ? f.committed() : f
  }

  private send(ev: JsonEvent): Flow {
    this.forwarded = true
    return this.sink.event(ev)
  }

  tableEvent(ev: TableEvent): Flow {
    switch (ev?.type) {
      case 'schema':
        return this.schema(ev.columns)
      case 'row':
        return this.row(ev.cells)
      case 'end':
        return this.end()
      default:
        throw this.fail(Fail.protocol(`not a table event: ${JSON.stringify((ev as any)?.type)}`))
    }
  }

  private schema(columns: readonly PublicColumn[]): Flow {
    if ('rows' === this.phase) throw this.fail(Fail.protocol('a second schema'))
    if ('done' === this.phase) throw this.fail(Fail.protocol('a schema after the end'))
    const labels = columns.map((c) => c.label)
    this.labels = labels
    this.keys = labels.map((l) => Object.freeze(Ev.key(l)))
    this.nextSame = labels.map((l, i) => {
      for (let j = i + 1; j < labels.length; j++) if (labels[j] === l) return j
      return -1
    })
    this.phase = 'rows'
    return this.send(Ev.arrayStart)
  }

  private row(cells: readonly Cell[]): Flow {
    if ('before_schema' === this.phase) throw Fail.protocol('a row before the schema')
    if ('done' === this.phase) throw this.fail(Fail.protocol('a row after the end'))
    if (cells.length !== this.labels.length) {
      throw this.fail(
        Fail.protocol(
          `row ${this.count + 1} has ${cells.length} cells; the schema has ${this.labels.length} columns`,
        ),
      )
    }
    // Before any of the row is forwarded, so a row is emitted whole or not
    // at all, as the CSV renderer renders it.
    for (let i = 0; i < cells.length; i++) {
      const type = cells[i]?.type
      if ('missing' === type && 'error' === this.missing) {
        throw this.fail(
          new Fail(
            'MISSING_VALUE',
            `row ${this.count + 1} has no value for column ${JSON.stringify(this.labels[i])}`,
          ),
        )
      }
      if ('null' !== type && 'bool' !== type && 'number' !== type && 'string' !== type && 'missing' !== type) {
        throw this.fail(
          Fail.protocol(
            `row ${this.count + 1} has no cell this protocol defines in column ${JSON.stringify(this.labels[i])}`,
          ),
        )
      }
    }
    const missing = this.missing
    if ('stop' === this.send(Ev.objectStart)) return 'stop'
    for (let i = 0; i < cells.length; i++) {
      const cell = cells[i]
      if (!contributes(cell, missing)) continue
      // A repeated label is written from the last column whose cell
      // contributes; an earlier column's value is superseded only by a
      // member that will actually be there.
      let later = this.nextSame[i]
      while (-1 !== later && !contributes(cells[later], missing)) later = this.nextSame[later]
      if (-1 !== later) continue
      let value: JsonEvent
      switch (cell.type) {
        case 'null':
          value = Ev.null
          break
        case 'bool':
          value = Ev.bool(cell.value)
          break
        case 'number':
          value = Ev.number(cell.value, cell.lexeme)
          break
        case 'string':
          value = Ev.string(cell.value)
          break
        default:
          // `missing` under `null`: `skip` does not contribute and was
          // passed over above; `error` was rejected before the row began.
          value = Ev.null
      }
      if ('stop' === this.send(this.keys[i])) return 'stop'
      if ('stop' === this.send(value)) return 'stop'
    }
    if ('stop' === this.send(Ev.objectEnd)) return 'stop'
    this.count++
    return 'continue'
  }

  private end(): Flow {
    if ('before_schema' === this.phase) throw Fail.protocol('the end before the schema')
    if ('done' === this.phase) throw this.fail(Fail.protocol('a second end'))
    if ('stop' === this.send(Ev.arrayEnd)) return 'stop'
    // Done only once the end has been taken downstream: a sink that failed
    // on it has not seen the document end.
    const flow = this.send(Ev.end)
    this.phase = 'done'
    return flow
  }
}
