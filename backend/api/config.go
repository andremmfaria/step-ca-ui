package api

import (
	"context"

	"step-ui/handlers"
	appmw "step-ui/middleware"
	"step-ui/openapi"
)

// roleLevels mirrors middleware.RequireRole's unexported map. Phase 1 hoists
// both into one shared location (5.5's role golden table); Phase 0 only
// needs GET /api/v1/config to report it.
var roleLevels = map[string]int{"viewer": 1, "manager": 2, "admin": 3}

type configBody struct {
	OIDCEnabled               bool           `json:"oidcEnabled"`
	OIDCButtonLabel           string         `json:"oidcButtonLabel"`
	ACMEEnabled               bool           `json:"acmeEnabled"`
	AppVersion                string         `json:"appVersion"`
	ContractSha               string         `json:"contractSha"`
	RoleLevels                map[string]int `json:"roleLevels"`
	SessionIdleTimeoutSeconds int            `json:"sessionIdleTimeoutSeconds"`
	ExpiringSoonDays          int            `json:"expiringSoonDays"`
}

type configOutput struct {
	Body configBody
}

// getConfig implements the public GET /api/v1/config (auth: public, no
// session touched). oidcButtonLabel and acmeEnabled have no backing config
// field yet — no OIDC-button-copy or ACME-toggle env var exists in
// config.Config today — so they are spike placeholders a later phase
// replaces with real settings.
func getConfig(h *handlers.Handler) func(context.Context, *struct{}) (*configOutput, error) {
	return func(_ context.Context, _ *struct{}) (*configOutput, error) {
		cfg := h.Cfg()
		oidcLabel := ""
		if cfg.OIDCEnabled {
			oidcLabel = "Continue with SSO"
		}
		return &configOutput{Body: configBody{
			OIDCEnabled:               cfg.OIDCEnabled,
			OIDCButtonLabel:           oidcLabel,
			ACMEEnabled:               true,
			AppVersion:                handlers.Version,
			ContractSha:               openapi.Sha256Hex,
			RoleLevels:                roleLevels,
			SessionIdleTimeoutSeconds: int(appmw.SessionTimeout.Seconds()),
			ExpiringSoonDays:          handlers.ExpiringSoonDays,
		}}, nil
	}
}
