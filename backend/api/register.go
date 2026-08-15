package api

import (
	"net/http"

	"step-ui/handlers"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires Phase 0's operations onto humaAPI against h. Kept separate
// from construction (Mount, NewForSpec) so cmd/openapi can call it against a
// Handler built with no database, no CA client and no environment (D3).
func Register(humaAPI huma.API, h *handlers.Handler) {
	// The one auth: optional operation in the API (5.5): it validates a
	// session when present, never answers 401, and is exempt from the
	// sliding-window renewal so an idling open tab cannot keep a session alive.
	huma.Register(humaAPI, optionalAuthOp(huma.Operation{
		OperationID: "getSession",
		Method:      http.MethodGet,
		Path:        BasePath + "/session",
		Tags:        []string{"session"},
		Summary:     "Get the current session state",
	}), getSession(h))

	huma.Register(humaAPI, publicOp(huma.Operation{
		OperationID: "getConfig",
		Method:      http.MethodGet,
		Path:        BasePath + "/config",
		Tags:        []string{"system"},
		Summary:     "Get public runtime configuration",
	}), getConfig(h))

	huma.Register(humaAPI, roleOp("viewer", huma.Operation{
		OperationID: "getStatus",
		Method:      http.MethodGet,
		Path:        BasePath + "/status",
		Tags:        []string{"dashboard"},
		Summary:     "Get active/expiring certificate counts",
	}), getStatus(h))

	// The two spike operations prove the octet-stream and multipart mechanics
	// Phase 4 reuses (5.7). They carry a real role rather than staying public:
	// an unauthenticated multipart upload on a certificate authority is not
	// something to leave reachable for the length of a migration.
	huma.Register(humaAPI, roleOp("viewer", huma.Operation{
		OperationID: "getSpikeBlob",
		Method:      http.MethodGet,
		Path:        BasePath + "/_spike/blob",
		Tags:        []string{"system"},
		Summary:     "Phase 0 spike: binary download mechanic",
		Responses:   spikeBlobResponses,
	}), getSpikeBlob)

	huma.Register(humaAPI, roleOp("manager", huma.Operation{
		OperationID: "postSpikeUpload",
		Method:      http.MethodPost,
		Path:        BasePath + "/_spike/upload",
		Tags:        []string{"system"},
		Summary:     "Phase 0 spike: multipart upload mechanic",
	}), postSpikeUpload)
}
