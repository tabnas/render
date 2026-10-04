# @tabnas/render

Streaming text, JSON, CSV, and record renderers for Tabnas event streams.

```sh
npm install @tabnas/render
```

The protocols it renders, `Fail` and its codes, the text boundary
(`TextOut`, `Writer`) and the renderers' options (`CsvOptions`,
`JsonOptions` and the rest) are `@tabnas/alchemy`'s shared types, imported
from `@tabnas/alchemy/shared`, a peer, and re-exported here where they were
always exported. `renderers` is this package's renderers and text stages as
alchemy's `Renderers`, which a host passes to alchemy's `compile` with
`@tabnas/transduce`'s `routers`.

See the [project README](https://github.com/tabnas/render#readme) for the API,
protocol contracts, limits, and examples.

This package includes its TypeScript sources under `src/` alongside the
compiled JavaScript and declarations under `dist/`.
