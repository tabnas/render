/* Copyright (c) 2026 tabnas, MIT License */

// This package's renderers and text stages as alchemy's `Renderers`: what
// an alchemy program's runtime builds its text output from. Alchemy
// declares the interface (`@tabnas/alchemy/shared`) and imports no
// renderer; a host passes `renderers` to its `compile`, with transduce's
// `routers`: `compile(src, file, { routers, renderers })`.

import {
  CoalescingOut,
  CsvOptions,
  JoinOut,
  JsonOptions,
  MissingRecord,
  Renderers,
  Sink,
  TableSink,
  TextOut,
  Writer,
} from '@tabnas/alchemy/shared'

import { CsvRenderer } from './csv'
import { JsonRenderer } from './json'
import { writeValue } from './number'
import { RecordsToJson } from './records'
import { Join, ReplaceText, WriteOut, hasCommitted } from './text'

// Each method constructs the renderer or stage it names with the same
// parameters, or calls the function it names.
export const renderers: Renderers = Object.freeze({
  json(out: TextOut, options?: Partial<JsonOptions>): Sink {
    return new JsonRenderer(out, options)
  },

  csv(out: TextOut, options?: Partial<CsvOptions>): TableSink {
    return new CsvRenderer(out, options)
  },

  recordsToJson(sink: Sink, missing?: MissingRecord): TableSink {
    return new RecordsToJson(sink, missing)
  },

  join(out: TextOut, separator: string): JoinOut {
    return new Join(out, separator)
  },

  replaceText(out: TextOut, from: string, to: string): TextOut {
    return new ReplaceText(out, from, to)
  },

  writeOut(writer: Writer): CoalescingOut {
    return new WriteOut(writer)
  },

  hasCommitted(out: TextOut): boolean {
    return hasCommitted(out)
  },

  writeValue(value: number): string {
    return writeValue(value)
  },
})
