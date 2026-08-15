package api

import (
	"context"

	"step-ui/handlers"
)

type statusBody struct {
	Total        int                       `json:"total"`
	ExpiringSoon []handlers.StatusExpiring `json:"expiringSoon"`
}

type statusOutput struct {
	Body statusBody
}

// getStatus ports h.APIStatus (main.go's GET /api/status) to GET
// /api/v1/status. It declares role=viewer and does no session work of its
// own: sessionMiddleware has already validated, and answered 401 with a
// problem document rather than a redirect, before this runs. Phase 0 wrote
// that check inline here; Phase 1 is where it moves into the chain.
func getStatus(h *handlers.Handler) func(context.Context, *struct{}) (*statusOutput, error) {
	return func(_ context.Context, _ *struct{}) (*statusOutput, error) {
		total, expiring := h.StatusSummary()
		return &statusOutput{Body: statusBody{Total: total, ExpiringSoon: expiring}}, nil
	}
}
