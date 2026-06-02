package handlers

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"time"

	"step-ui/config"
	"step-ui/models"
	"step-ui/security"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"

	appdb "step-ui/db"
)

// StartedAt records the time the server process started (set in main).
var StartedAt time.Time

// Versioning — overridden via ldflags at build time
var (
	Version   = "1.6.0"
	BuildDate = "2026-05-28"
	GitCommit = "unknown"
)

// Handler holds the application dependencies for all HTTP handlers.
type Handler struct {
	db    *sql.DB
	cfg   *config.Config
	store *sessions.CookieStore
	tmpls map[string]*template.Template
	// assets is the embedded FS rooted at the module root (contains templates/
	// and static/ sub-trees).  When nil, loadTemplates falls back to reading
	// from the filesystem at the current working directory.
	assets fs.FS

	// OIDC fields — non-nil only when cfg.OIDCEnabled is true
	oidcOAuth2Config *oauth2.Config
	oidcVerifier     *gooidc.IDTokenVerifier
}

// New creates a Handler that reads templates from the filesystem (CWD-relative).
// Prefer NewWithFS in production to embed assets into the binary.
func New(db *sql.DB, cfg *config.Config, store *sessions.CookieStore) *Handler {
	return NewWithFS(db, cfg, store, nil)
}

// NewWithFS creates a Handler that reads templates from the provided FS.
// Pass the module-root embed.FS so templates and static files are baked into
// the binary and the working directory no longer matters at runtime.
func NewWithFS(db *sql.DB, cfg *config.Config, store *sessions.CookieStore, assets fs.FS) *Handler {
	h := &Handler{db: db, cfg: cfg, store: store, tmpls: make(map[string]*template.Template), assets: assets}
	h.loadTemplates()
	if cfg.OIDCEnabled {
		h.initOIDC()
	}
	return h
}

// initOIDC discovers the provider and wires up the oauth2 config + token verifier.
// Called only when OIDCEnabled is true.
func (h *Handler) initOIDC() {
	ctx := context.Background()
	provider, err := gooidc.NewProvider(ctx, h.cfg.OIDCIssuerURL)
	if err != nil {
		log.Fatalf("OIDC provider discovery failed for %s: %v", h.cfg.OIDCIssuerURL, err)
	}
	h.oidcOAuth2Config = &oauth2.Config{
		ClientID:     h.cfg.OIDCClientID,
		ClientSecret: h.cfg.OIDCClientSecret,
		RedirectURL:  h.cfg.OIDCRedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email", "groups"},
	}
	h.oidcVerifier = provider.Verifier(&gooidc.Config{ClientID: h.cfg.OIDCClientID})
	slog.Info("OIDC enabled", "issuer", h.cfg.OIDCIssuerURL)
}

func (h *Handler) loadTemplates() {
	funcs := h.templateFuncs()
	pages := []string{
		"home",
		"dashboard",
		"certificates",
		"certificate_detail",
		"issue",
		"import",
		"provisioners",
		"history",
		"admin",
		"profile",
		"le_dashboard",
		"le_issue",
		"le_settings",
		"le_logs",
		"admin_users",
		"admin_user_profile",
		"admin_users_temp",
		"admin_activity",
		"admin_security",
		"admin_console",
		"admin_about",
		"admin_integrity",
		"admin_backup",
		"admin_notifications",
		"profile_2fa",
	}
	for _, page := range pages {
		baseFile := "templates/base.html"
		baseName := "base.html"
		if len(page) >= 6 && page[:6] == "admin_" || page == "admin" {
			baseFile = "templates/admin_base.html"
			baseName = "admin_base.html"
		}
		pageFile := fmt.Sprintf("templates/%s.html", page)
		var (
			t   *template.Template
			err error
		)
		if h.assets != nil {
			t, err = template.New(baseName).Funcs(funcs).ParseFS(h.assets, baseFile, pageFile)
		} else {
			t, err = template.New(baseName).Funcs(funcs).ParseFiles(baseFile, pageFile)
		}
		if err != nil {
			slog.Error("template parse error", "page", page, "err", err)
			continue
		}
		h.tmpls[page] = t
	}
	var (
		loginTmpl *template.Template
		loginErr  error
	)
	if h.assets != nil {
		loginTmpl, loginErr = template.New("login.html").Funcs(funcs).ParseFS(h.assets, "templates/login.html")
	} else {
		loginTmpl, loginErr = template.New("login.html").Funcs(funcs).ParseFiles("templates/login.html")
	}
	if loginErr == nil {
		h.tmpls["login"] = loginTmpl
	} else {
		slog.Error("login template parse error", "err", loginErr)
	}
}

func (h *Handler) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"daysLeft": func(t *time.Time) int {
			if t == nil {
				return 999
			}
			return int(time.Until(*t).Hours() / 24)
		},
		"badgeClass": func(t *time.Time) string {
			if t == nil {
				return "ok"
			}
			d := int(time.Until(*t).Hours() / 24)
			if d <= 0 {
				return "danger"
			}
			if d <= 30 {
				return "warn"
			}
			return "ok"
		},
		"fmtTime": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"fmtLog": func(t time.Time) string {
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"hasRole": func(role, minRole string) bool {
			levels := map[string]int{"viewer": 1, "manager": 2, "admin": 3}
			return levels[role] >= levels[minRole]
		},
		"isActive": func(page, current string) string {
			if page == current {
				return "active"
			}
			return ""
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"deref": func(s *string) string {
			if s == nil {
				return "—"
			}
			return *s
		},
		"contains": func(arr []string, v string) bool {
			for _, s := range arr {
				if s == v {
					return true
				}
			}
			return false
		},
		"seq": func(start, end int) []int {
			var s []int
			for i := start; i <= end; i++ {
				s = append(s, i)
			}
			return s
		},
	}
}

func (h *Handler) sess(r *http.Request) *sessions.Session {
	s, err := h.store.Get(r, "step-ui")
	if err != nil {
		slog.Warn("session decode failed", "remote", r.RemoteAddr, "host", r.Host, "path", r.URL.Path, "err", err)
	}
	return s
}

func (h *Handler) sessionInfo(r *http.Request) *models.SessionInfo {
	s := h.sess(r)
	id, _ := s.Values["user_id"].(int)
	username, _ := s.Values["username"].(string)
	role, _ := s.Values["role"].(string)
	theme := "dark"
	if id > 0 {
		if u, err := appdb.GetUserByID(h.db, id); err == nil && u != nil && u.Theme != "" {
			theme = u.Theme
		}
	}
	return &models.SessionInfo{UserID: id, Username: username, Role: role, Theme: theme}
}

func (h *Handler) flash(w http.ResponseWriter, r *http.Request, t, text string) {
	s := h.sess(r)
	s.AddFlash(models.FlashMsg{Type: t, Text: text})
	_ = s.Save(r, w)
}

func (h *Handler) popFlash(w http.ResponseWriter, r *http.Request) []models.FlashMsg {
	s := h.sess(r)
	flashes := s.Flashes()
	_ = s.Save(r, w)
	var msgs []models.FlashMsg
	for _, f := range flashes {
		if m, ok := f.(models.FlashMsg); ok {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) string {
	s, err := h.store.Get(r, "step-ui")
	if err != nil {
		slog.Warn("session reset after decode failure", "remote", r.RemoteAddr, "host", r.Host, "path", r.URL.Path, "err", err)
		s, _ = h.store.New(r, "step-ui")
	}
	token, ok := s.Values["csrf_token"].(string)
	if !ok || token == "" {
		token = security.GenerateToken()
		s.Values["csrf_token"] = token
		_ = s.Save(r, w)
	}
	return token
}

func (h *Handler) csrfOK(r *http.Request) bool {
	s := h.sess(r)
	token := r.FormValue("csrf_token")
	sess, _ := s.Values["csrf_token"].(string)
	// Guard against empty tokens before the constant-time comparison so an
	// empty session token cannot be matched by an empty form value.
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(sess)) == 1
}

func (h *Handler) requireCSRF(w http.ResponseWriter, r *http.Request, redirectTo string) bool {
	if h.csrfOK(r) {
		return true
	}
	h.flash(w, r, "err", "Session error. Please refresh the page.")
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
	return false
}

func (h *Handler) base(w http.ResponseWriter, r *http.Request, activePage string) map[string]interface{} {
	return map[string]interface{}{
		"Session":    h.sessionInfo(r),
		"Msgs":       h.popFlash(w, r),
		"ActivePage": activePage,
		"CSRFToken":  h.csrf(w, r),
	}
}

func (h *Handler) render(w http.ResponseWriter, page string, data map[string]interface{}) {
	tmpl, ok := h.tmpls[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := "layout"
	if page == "login" {
		name = "login.html"
	} else if page == "admin" || (len(page) >= 6 && page[:6] == "admin_") {
		name = "admin_layout"
	}
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "page", page, "err", err)
	}
}
