package api

import (
	"github.com/danielgtaylor/huma/v2"
)

// stripSubmittedValues removes the submitted value huma echoes back in every
// errors[] entry (5.2).
//
// huma.ErrorDetail carries Value to help a client debug, and on this API that
// is a disclosure: a malformed login body puts the submitted password into a
// problem document, which the SPA then surfaces in a toast and which any error
// reporter or e2e artifact carries onward. Validation feedback names the
// field, never the content.
//
// It is installed in config() rather than on the mounted API so the spec-only
// API built by cmd/openapi has identical behaviour and the two cannot diverge.
func stripSubmittedValues(_ huma.Context, _ string, v any) (any, error) {
	model, ok := v.(*huma.ErrorModel)
	if !ok {
		return v, nil
	}
	for _, detail := range model.Errors {
		if detail != nil {
			detail.Value = nil
		}
	}
	return v, nil
}
