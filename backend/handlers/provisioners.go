package handlers

import (
	"net/http"

	"step-ui/stepca"
)

// Provisioners renders the CA provisioners list page.
func (h *Handler) Provisioners(w http.ResponseWriter, r *http.Request) {
	var provs []map[string]interface{}
	if caClient, err := h.caClient(); err == nil {
		if list, err := caClient.Provisioners(r.Context()); err == nil {
			provs = provisionerMaps(list)
		}
	}
	data := h.base(w, r, "prov")
	data["Provisioners"] = provs
	data["CAURL"] = h.cfg.CAURL
	data["RootCert"] = h.cfg.RootCert
	data["Provisioner"] = h.cfg.Provisioner
	h.render(w, "provisioners", data)
}

// provisionerMaps converts the typed stepca.ProvisionerInfo list into the
// []map[string]interface{}{"name":..., "type":...} shape provisioners.html
// (index . "name" / index . "type") already expects — kept as the template
// contract instead of switching the template to typed struct access, since
// the template has no other dependents that would benefit (see plan's
// Reasoning Transparency).
func provisionerMaps(list []stepca.ProvisionerInfo) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]interface{}{"name": p.Name, "type": p.Type})
	}
	return out
}
