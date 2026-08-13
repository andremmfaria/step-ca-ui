package api

import (
	"context"

	"step-ui/handlers"

	"github.com/danielgtaylor/huma/v2"
)

type statusBody struct {
	Total        int                       `json:"total"`
	ExpiringSoon []handlers.StatusExpiring `json:"expiringSoon"`
}

type statusOutput struct {
	Body statusBody
}

// getStatus ports h.APIStatus (main.go's GET /api/status) to GET
// /api/v1/status. It requires a valid session, unlike GET /api/v1/session:
// this is the operation Phase 0 uses to prove a protected route answers
// 401 application/problem+json rather than a redirect.
func getStatus(h *handlers.Handler) func(context.Context, *struct{}) (*statusOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		r, _ := httpFrom(ctx)

		s, err := h.Store().Get(r, handlers.SessionCookieName)
		if err != nil {
			return nil, huma.Error401Unauthorized("authentication required")
		}
		if _, ok := validatedUser(h, s); !ok {
			return nil, huma.Error401Unauthorized("authentication required")
		}

		total, expiring := h.StatusSummary()
		return &statusOutput{Body: statusBody{Total: total, ExpiringSoon: expiring}}, nil
	}
}
