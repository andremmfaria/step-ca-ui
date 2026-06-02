package handlers

import (
	"encoding/json"
	"net/http"
)

// Provisioners renders the CA provisioners list page.
func (h *Handler) Provisioners(w http.ResponseWriter, r *http.Request) {
	var provs []map[string]interface{}
	out, err := runStep(r.Context(), h.cfg, execRunner, []string{"ca", "provisioner", "list"}, nil, nil)
	if err == nil {
		_ = json.Unmarshal(out, &provs)
	}
	data := h.base(w, r, "prov")
	data["Provisioners"] = provs
	data["CAURL"] = h.cfg.CAURL
	data["RootCert"] = h.cfg.RootCert
	data["Provisioner"] = h.cfg.Provisioner
	h.render(w, "provisioners", data)
}
