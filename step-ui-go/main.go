// Package main is the entry point for the Step-CA UI web application.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"step-ui/config"
	"step-ui/handlers"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/sessions"

	appdb "step-ui/db"

	mw "step-ui/middleware"
)

//go:embed templates static
var embeddedAssets embed.FS

// mimeByExt maps file extensions to correct Content-Type values.
// http.FileServer/ServeContent may use system /etc/mime.types which maps .css
// to text/plain on some distros — this table overrides that.
var mimeByExt = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".mjs":   "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".map":   "application/json; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
}

// staticHandlerFromFS serves static files from an fs.FS with enforced MIME types
// and path-traversal protection.  The FS should be rooted at the "static/"
// sub-tree (use fs.Sub to strip the prefix before passing here).
func staticHandlerFromFS(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path and reject traversal attempts.
		clean := filepath.Clean("/" + r.URL.Path)
		// Strip leading slash — fs.FS paths are relative.
		relPath := strings.TrimPrefix(clean, "/")
		if relPath == "" || strings.HasPrefix(relPath, "..") {
			http.NotFound(w, r)
			return
		}
		f, err := fsys.Open(relPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		st, err := fs.Stat(fsys, relPath)
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		ext := strings.ToLower(filepath.Ext(relPath))
		if mt, ok := mimeByExt[ext]; ok {
			w.Header().Set("Content-Type", mt)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = io.Copy(w, f)
	})
}

func init() {
	// Принудительно регистрируем корректные MIME-типы для статики.
	// http.ServeContent использует mime.TypeByExtension() и перезаписывает
	// любой ранее установленный Content-Type, поэтому только этот способ работает.
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".webp", "image/webp")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
	_ = mime.AddExtensionType(".otf", "font/otf")
	_ = mime.AddExtensionType(".map", "application/json; charset=utf-8")
}

func main() {
	handlers.StartedAt = time.Now()
	// Регистрируем типы для gob (gorilla/sessions)
	gob.Register(int(0))
	gob.Register(int64(0))
	gob.Register("")
	cfg := config.Load()

	// ─── Startup security checks ─────────────────────────────────────────────
	const weakKey = "change-me-in-production-32chars!"
	if cfg.SecretKey == weakKey || len(cfg.SecretKey) < 32 {
		log.Fatal("FATAL: SECRET_KEY is the default or shorter than 32 chars — set a strong SECRET_KEY before starting")
	}
	if !cfg.SessionSecure {
		slog.Warn("SESSION_SECURE=false: session cookies will not carry the Secure flag; do not use this in production")
	}

	// ─── Database ────────────────────────────────────────────────────────────
	conn, err := appdb.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Cannot connect to database: %v", err)
	}
	// Run schema migrations before registering the close defer; log.Fatalf
	// calls os.Exit which skips defers, so we keep the conn available and
	// only register the defer after the startup sequence completes.
	if err := appdb.InitSchema(conn); err != nil {
		_ = conn.Close()
		log.Fatalf("Cannot init DB schema: %v", err)
	}
	if err := appdb.InitLESchema(conn); err != nil {
		_ = conn.Close()
		log.Fatalf("Cannot init LE schema: %v", err)
	}
	if err := appdb.InitNotificationSchema(conn); err != nil {
		_ = conn.Close()
		log.Fatalf("Cannot init notification schema: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// ─── Sessions ────────────────────────────────────────────────────────────
	hashKey := sha256.Sum256([]byte(cfg.SecretKey))
	blockKey := sha256.Sum256([]byte(cfg.SecretKey + "_block"))
	store := sessions.NewCookieStore(hashKey[:], blockKey[:16])
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   28800,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cfg.SessionSecure,
	}

	// ─── Handlers ────────────────────────────────────────────────────────────
	// Pass the embedded FS so templates and static files are read from the
	// binary rather than from the working directory at runtime.
	h := handlers.NewWithFS(conn, cfg, store, embeddedAssets)

	// ─── Let's Encrypt auto-renewer ──────────────────────────────────────────
	h.StartRenewer()
	h.StartNotificationWorker()

	// ─── Router ──────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chiMiddleware.Recoverer)
	// RealIP rewrites r.RemoteAddr from X-Forwarded-For / X-Real-IP headers.
	// Only enable when the app sits behind a trusted reverse proxy; leaving it
	// off means the rate limiter and auth log always see the real socket peer
	// and cannot be spoofed by a client crafting forwarding headers.
	if cfg.TrustProxy {
		r.Use(chiMiddleware.RealIP)
	}
	r.Use(mw.SecurityHeaders(cfg.EnableHSTS))

	// Публичные маршруты
	r.Get("/health", h.Liveness)
	r.Get("/ready", h.Readiness)
	r.Get("/login", h.LoginGet)
	r.Post("/login", h.LoginPost)
	r.Get("/logout", h.Logout)
	if cfg.OIDCEnabled {
		r.Get("/auth/oidc/login", h.OIDCLogin)
		r.Get("/auth/oidc/callback", h.OIDCCallback)
	}

	// Авторизованные маршруты
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireLogin(store))

		r.Get("/", h.Home)
		r.Get("/dashboard", h.Dashboard)
		r.Get("/api/status", h.APIStatus)

		// Сертификаты (viewer+)
		r.Get("/certificates", h.Certificates)
		r.Get("/certificates/{id}", h.CertificateDetails)
		r.Get("/history", h.History)
		r.Get("/provisioners", h.Provisioners)

		// Скачать CA cert (admin)
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin", store))
			r.Get("/download/ca", h.DownloadCA)
			r.Get("/download/intermediate-ca", h.DownloadIntermediateCA)
			r.Get("/download/full-chain", h.DownloadFullChain)
		})

		// Операции с сертификатами (manager+)
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("manager", store))
			r.Get("/issue", h.IssueGet)
			r.Post("/issue", h.IssuePost)
			r.Post("/renew/{id}", h.Renew)
			r.Get("/import", h.ImportGet)
			r.Post("/import", h.ImportPost)
			r.Get("/download/cert/{id}", h.DownloadCert)
			r.Get("/download/key/{id}", h.DownloadKey)
		})

		// Отзыв (admin)
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin", store))
			r.Post("/revoke/{id}", h.Revoke)
		})

		// Управление пользователями (admin)
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin", store))
			// Админ-пространство
			r.Get("/admin", h.AdminGet)
			r.Get("/admin/users", h.Users)
			r.Post("/admin/users", h.UsersPost)
			r.Get("/admin/users/{id}", h.UserProfile)
			r.Get("/admin/users-temp", h.AdminUsersTempGet)
			r.Post("/admin/users-temp", h.AdminUsersTempPost)
			r.Get("/admin/activity", h.AdminActivityGet)
			r.Get("/admin/security", h.SecurityLog)
			r.Get("/admin/console", h.AdminConsoleGet)
			r.Get("/admin/about", h.AdminAboutGet)
			r.Get("/admin/integrity", h.AdminIntegrityGet)
			r.Get("/admin/backup", h.AdminBackupGet)
			r.Post("/admin/backup/download", h.AdminBackupDownload)
			r.Get("/admin/notifications", h.AdminNotificationsGet)
			r.Post("/admin/notifications", h.AdminNotificationsPost)
			r.Post("/admin/notifications/test", h.AdminNotificationsTest)
		})

		// Let's Encrypt (manager+)
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("manager", store))
			r.Get("/le", h.LEDashboard)
			r.Get("/le/issue", h.LEIssueGet)
			r.Post("/le/issue", h.LEIssuePost)
			r.Post("/le/{id}/renew", h.LERenew)
			r.Post("/le/{id}/delete", h.LEDelete)
			r.Post("/le/{id}/autorenew", h.LEToggleAutoRenew)
			r.Get("/le/download/cert/{id}", h.LEDownloadCert)
			r.Get("/le/download/key/{id}", h.LEDownloadKey)
			r.Get("/le/settings", h.LESettingsGet)
			r.Post("/le/settings", h.LESettingsPost)
			r.Get("/le/logs", h.LELogs)
		})

		// Профиль (любой авторизованный)
		r.Get("/profile", h.ProfileGet)
		r.Post("/profile", h.ProfilePost)
		r.Get("/profile/2fa", h.Profile2FAGet)
		r.Post("/profile/2fa/start", h.Profile2FAStart)
		r.Get("/profile/2fa/qr", h.Profile2FAQR)
		r.Post("/profile/2fa/confirm", h.Profile2FAConfirm)
		r.Post("/profile/2fa/disable", h.Profile2FADisable)
	})

	// ─── Static files ─────────────────────────────────────────────────────────
	// Use the embedded FS sub-tree for static files so the binary is self-contained
	// and does not depend on the working directory.  staticHandlerFromFS enforces
	// MIME types and path-traversal protection on top of the embed.FS boundary.
	staticFS, fsErr := fs.Sub(embeddedAssets, "static")
	if fsErr != nil {
		// conn.Close() defer will run before this panic.
		panic("cannot create static sub-FS: " + fsErr.Error())
	}
	r.Handle("/static/*", http.StripPrefix("/static/", staticHandlerFromFS(staticFS)))
	// ─── Start server ─────────────────────────────────────────────────────────
	for _, dir := range []string{cfg.CertsDir, cfg.UploadDir, "/opt/step-ui/ssl", "/opt/step-ui/data"} {
		_ = os.MkdirAll(dir, 0o750) //nolint:gosec // G301: app data dirs; restrictive perms appropriate for non-root service user
	}

	// temp_users_expire_ticker — panic-safe background goroutine
	handlers.SafeGoExported("temp-users-expiry", func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for range t.C {
			if n, err := appdb.ExpireOverdueTempUsers(conn); err == nil && n > 0 {
				slog.Info("temp users expired", "count", n)
			}
		}
	})

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)

	// useHTTPS: explicit config flag takes precedence; falls back to probing
	// whether the SSL cert file exists (preserves original auto-detect behaviour).
	useHTTPS := cfg.UseHTTPS || func() bool {
		_, err := os.Stat(cfg.SSLCert)
		return err == nil
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful-shutdown: block on SIGINT/SIGTERM then drain in-flight requests.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvErr := make(chan error, 1)
	if useHTTPS {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		slog.Info("starting Step-CA UI (HTTPS)", "port", cfg.Port)
		go func() { srvErr <- srv.ListenAndServeTLS(cfg.SSLCert, cfg.SSLKey) }()
	} else {
		slog.Warn("starting Step-CA UI (HTTP) — not suitable for production", "port", cfg.Port)
		go func() { srvErr <- srv.ListenAndServe() }()
	}

	select {
	case err := <-srvErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Use slog+os.Exit so defers (stop/conn.Close) can run before exit.
			slog.Error("server error", "err", err)
			stop()
			_ = conn.Close()
			os.Exit(1) //nolint:gocritic // exitAfterDefer: stop() called explicitly above
		}
	case <-ctx.Done():
		stop()
		slog.Info("shutdown signal received; draining in-flight requests")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("graceful shutdown error", "err", err)
		}
		_ = conn.Close()
		slog.Info("server stopped cleanly")
	}
}
