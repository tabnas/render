module github.com/tabnas/render/go

go 1.24.7

// The protocols this package renders, declared in alchemy's shared
// package, which alchemy carries from v0.2.0.
require github.com/tabnas/alchemy/go v0.2.4

// The engine and transduce's Go port, through which the read-back oracles
// parse a document into events (transduce builds on alchemy's shared
// package from v0.2.0).
require (
	github.com/tabnas/parser/go v0.12.11
	github.com/tabnas/transduce/go v0.2.5
)

// The grammars the read-back oracles parse with, and the shared fixture
// runner.
require (
	github.com/tabnas/csv/go v0.6.5
	github.com/tabnas/json/go v0.5.16
	github.com/tabnas/jsonl/go v0.1.15
	github.com/tabnas/support/go v0.3.9
	github.com/tabnas/yaml/go v0.5.22
)

require github.com/tabnas/jsonic/go v0.7.8 // indirect
