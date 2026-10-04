// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

// shared.go: the types the renderers face, under this package's names.
//
// TextOut, the CSV dialect and the JSON profile are declared in alchemy's
// shared package, github.com/tabnas/alchemy/go/shared, beside the
// protocols this package renders, so that alchemy can name them without
// depending on this package. Each is declared here again under its own
// name: a type as an alias, a constant as itself, a function as a call. So
// this package's API is the one it had.

import (
	"github.com/tabnas/alchemy/go/shared"
)

// TextOut is a consumer of text fragments (shared/text.go).
type TextOut = shared.TextOut

// The CSV dialect (shared/csv.go).
type (
	Newline    = shared.Newline
	Quoting    = shared.Quoting
	CSVOptions = shared.CSVOptions
)

// The record terminators and the quoting rules.
const (
	NewlineCRLF    = shared.NewlineCRLF
	NewlineLF      = shared.NewlineLF
	QuotingAlways  = shared.QuotingAlways
	QuotingMinimal = shared.QuotingMinimal
)

// DefaultCSVOptions is the standard profile: `,`, CRLF, a header, an
// empty null text, Missing an error, every field quoted.
func DefaultCSVOptions() CSVOptions { return shared.DefaultCSVOptions() }

// MissingAs is the CSVOptions.Missing that writes text for a CellMissing.
func MissingAs(text string) *string { return shared.MissingAs(text) }

// JSONOptions is the JSON profile (shared/json.go).
type JSONOptions = shared.JSONOptions
