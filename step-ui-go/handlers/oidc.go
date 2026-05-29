package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	appdb "step-ui/db"
)

// oidcRandomString returns a URL-safe random string of n bytes (base64 encoded).
func oidcRandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// mapGroupsToRole returns the highest-privilege role that matches one of the
// given groups. Precedence: admin > manager > viewer. Returns "" when no group
// matches and the caller must apply OIDCDefaultRole.
func (h *Handler) mapGroupsToRole(groups []string) string {
	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupSet[g] = struct{}{}
	}
	if h.cfg.OIDCGroupAdmin != "" {
		if _, ok := groupSet[h.cfg.OIDCGroupAdmin]; ok {
			return "admin"
		}
	}
	if h.cfg.OIDCGroupManager != "" {
		if _, ok := groupSet[h.cfg.OIDCGroupManager]; ok {
			return "manager"
		}
	}
	if h.cfg.OIDCGroupViewer != "" {
		if _, ok := groupSet[h.cfg.OIDCGroupViewer]; ok {
			return "viewer"
		}
	}
	return ""
}

// OIDCLogin initiates the OIDC authorization code + PKCE flow.
// GET /auth/oidc/login
func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.OIDCEnabled || h.oidcOAuth2Config == nil {
		http.Error(w, "OIDC not enabled", http.StatusNotFound)
		return
	}

	state := oidcRandomString(16)
	nonce := oidcRandomString(16)
	verifier := oidcRandomString(32)

	s := h.sess(r)
	s.Values["oidc_state"] = state
	s.Values["oidc_nonce"] = nonce
	s.Values["oidc_verifier"] = verifier
	s.Save(r, w)

	url := h.oidcOAuth2Config.AuthCodeURL(
		state,
		gooidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, url, http.StatusFound)
}

// OIDCCallback handles the provider redirect after user authentication.
// GET /auth/oidc/callback
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.OIDCEnabled || h.oidcOAuth2Config == nil || h.oidcVerifier == nil {
		http.Error(w, "OIDC not enabled", http.StatusNotFound)
		return
	}

	s := h.sess(r)
	ip := r.RemoteAddr

	// --- state check ---
	savedState, _ := s.Values["oidc_state"].(string)
	if savedState == "" || r.URL.Query().Get("state") != savedState {
		appdb.LogAuth(h.db, "", ip, false, "OIDC callback: state mismatch")
		h.flash(w, r, "err", "OIDC state mismatch — possible CSRF attack")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	savedNonce, _ := s.Values["oidc_nonce"].(string)
	savedVerifier, _ := s.Values["oidc_verifier"].(string)

	// clear one-time OIDC session values
	delete(s.Values, "oidc_state")
	delete(s.Values, "oidc_nonce")
	delete(s.Values, "oidc_verifier")
	s.Save(r, w)

	// --- exchange code ---
	ctx := context.Background()
	token, err := h.oidcOAuth2Config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(savedVerifier))
	if err != nil {
		appdb.LogAuth(h.db, "", ip, false, "OIDC token exchange failed: "+err.Error())
		h.flash(w, r, "err", "OIDC authentication failed")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// --- verify ID token ---
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		appdb.LogAuth(h.db, "", ip, false, "OIDC: no id_token in response")
		h.flash(w, r, "err", "OIDC authentication failed: missing id_token")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	idToken, err := h.oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		appdb.LogAuth(h.db, "", ip, false, "OIDC id_token verification failed: "+err.Error())
		h.flash(w, r, "err", "OIDC token verification failed")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// --- check nonce ---
	if idToken.Nonce != savedNonce {
		appdb.LogAuth(h.db, "", ip, false, "OIDC nonce mismatch")
		h.flash(w, r, "err", "OIDC nonce mismatch — replay attack detected")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// --- extract claims ---
	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		appdb.LogAuth(h.db, "", ip, false, "OIDC claims parse failed: "+err.Error())
		h.flash(w, r, "err", "OIDC claims error")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// preferred_username, fall back to email, then sub
	username := ""
	if v, ok := claims["preferred_username"].(string); ok && v != "" {
		username = v
	} else if v, ok := claims["email"].(string); ok && v != "" {
		username = v
	} else {
		username = idToken.Subject
	}

	displayName, _ := claims["name"].(string)
	if displayName == "" {
		displayName = username
	}

	// --- map groups to role ---
	var groups []string
	if raw, ok := claims[h.cfg.OIDCGroupClaim]; ok {
		switch v := raw.(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					groups = append(groups, s)
				}
			}
		case []string:
			groups = v
		}
	}

	role := h.mapGroupsToRole(groups)
	if role == "" {
		role = h.cfg.OIDCDefaultRole
	}
	if role == "" {
		appdb.LogAuth(h.db, username, ip, false, "OIDC: no matching group, access denied")
		h.flash(w, r, "err", "Access denied: your account is not in an authorised group")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// --- upsert user ---
	user, err := appdb.UpsertOIDCUser(h.db, username, displayName, role, h.cfg.OIDCSyncRole)
	if err != nil || user == nil {
		appdb.LogAuth(h.db, username, ip, false, "OIDC upsert failed: "+func() string {
			if err != nil {
				return err.Error()
			}
			return "nil user"
		}())
		h.flash(w, r, "err", "OIDC login error: could not create/update user account")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if !user.IsActive {
		appdb.LogAuth(h.db, username, ip, false, "OIDC: account inactive")
		h.flash(w, r, "err", "Account is disabled. Contact an administrator.")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// --- complete login (reuses auth.go:154 — sets session, clears rate limit, logs) ---
	h.completeLogin(w, r, user, "OIDC login")
	http.Redirect(w, r, "/", http.StatusFound)
}
