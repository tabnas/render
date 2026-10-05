// Copyright (c) 2026 tabnas, MIT License

package tabnasrender

import (
	"io"

	"github.com/tabnas/alchemy/go/shared"
)

// Renderers is this package's implementation of alchemy's
// shared.Renderers: the renderers, text stages and number functions
// alchemy's runtime uses, each this package's constructor or function. A
// host hands it to alchemy's Compile, with transduce's Routers.
func Renderers() shared.Renderers { return renderers{} }

type renderers struct{}

// JSON is NewJSONRenderer.
func (renderers) JSON(out TextOut, options JSONOptions) shared.Sink {
	return NewJSONRenderer[TextOut](out, options)
}

// CSV is NewCSVRenderer.
func (renderers) CSV(out TextOut, options CSVOptions) (shared.TableSink, *shared.Fail) {
	renderer, f := NewCSVRenderer[TextOut](out, options)
	if f != nil {
		return nil, f
	}
	return renderer, nil
}

// RecordsToJSON is NewRecordsToJSON.
func (renderers) RecordsToJSON(sink shared.Sink) shared.TableSink {
	return NewRecordsToJSON[shared.Sink](sink)
}

// Join is NewJoin.
func (renderers) Join(out TextOut, separator string) shared.JoinOut {
	return NewJoin[TextOut](out, separator)
}

// ReplaceText is NewReplaceText.
func (renderers) ReplaceText(out TextOut, from, to string) TextOut {
	return NewReplaceText[TextOut](out, from, to)
}

// WriteOut is NewWriteOut, with WithLimits and WithMetrics.
func (renderers) WriteOut(w io.Writer, limits shared.Limits, metrics *shared.Metrics) TextOut {
	return NewWriteOut(w).WithLimits(limits).WithMetrics(metrics)
}

// WriteValue is FormatValue.
func (renderers) WriteValue(value float64) string { return FormatValue(value) }
