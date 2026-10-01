module github.com/tabnas/render/go

go 1.24.7

// The protocols this package renders (transduce's Go port, not yet
// released: resolved through a go.work over the sibling checkout until it
// is), and the engine they are built on.
require (
	github.com/tabnas/parser/go v0.12.7
	github.com/tabnas/transduce/go v0.1.0
)

// The grammars the read-back oracles parse with, and the shared fixture
// runner.
require (
	github.com/tabnas/csv/go v0.5.11
	github.com/tabnas/json/go v0.5.11
	github.com/tabnas/jsonl/go v0.1.10
	github.com/tabnas/support/go v0.3.5
	github.com/tabnas/yaml/go v0.5.15
)

require github.com/tabnas/jsonic/go v0.7.2 // indirect
