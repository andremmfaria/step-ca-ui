package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"step-ui/config"
	"step-ui/models"
)

// sessionFlashes reads back the flashes a handler left in the session cookie,
// which is the path a browser takes.
func sessionFlashes(t *testing.T, h *Handler, rr *httptest.ResponseRecorder) []models.FlashMsg {
	t.Helper()
	req := testReq("GET", "/", "")
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	return h.popFlash(httptest.NewRecorder(), req)
}

// TestFlashSurvivesTheCookie pins V11. models.FlashMsg went unregistered with
// gob, so every h.flash in the application failed its Save, wrote no cookie,
// and was discarded. Nothing errored and no message was ever displayed.
func TestFlashSurvivesTheCookie(t *testing.T) {
	store := testStore()
	h := newTestHandler(&config.Config{}, store)

	writeReq := testReq("GET", "/somewhere", "")
	writeRR := httptest.NewRecorder()
	h.flash(writeRR, writeReq, "err", "the message a user must see")

	cookies := writeRR.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("h.flash wrote no cookie: sessions.Save failed, which is the V11 defect")
	}

	readReq := testReq("GET", "/next", "")
	for _, c := range cookies {
		readReq.AddCookie(c)
	}
	msgs := h.popFlash(httptest.NewRecorder(), readReq)
	if len(msgs) != 1 {
		t.Fatalf("popFlash: got %+v want exactly one message", msgs)
	}
	if msgs[0].Type != "err" || msgs[0].Text != "the message a user must see" {
		t.Errorf("round-tripped flash: got %+v", msgs[0])
	}
}

// TestUsersPost_InvalidRoleRejected is V9: an unrecognised role must be refused
// with a message rather than written to users.role, where it produces an
// account that logs in and then fails every role check. h.db is nil, so any
// case that reaches the database panics rather than passing quietly.
func TestUsersPost_InvalidRoleRejected(t *testing.T) {
	cases := map[string]string{
		"create":      "action=create&username=bob&password=Str0ng!Passw0rd&role=%s",
		"change_role": "action=change_role&uid=2&role=%s",
	}
	for action, form := range cases {
		for _, role := range []string{"superuser", "Admin", "", "admin viewer"} {
			t.Run(action+"/"+role, func(t *testing.T) {
				store := testStore()
				cookies, token := seedCSRF(t, store)
				h := newTestHandler(&config.Config{}, store)

				body := strings.Replace(form, "%s", role, 1) + "&csrf_token=" + token
				req := testReq("POST", "/admin/users", body)
				for _, c := range cookies {
					req.AddCookie(c)
				}
				rr := httptest.NewRecorder()
				h.UsersPost(rr, req)

				if rr.Code != http.StatusFound {
					t.Fatalf("expected 302, got %d", rr.Code)
				}
				msgs := sessionFlashes(t, h, rr)
				if len(msgs) != 1 || msgs[0].Type != "err" {
					t.Fatalf("flashes: got %+v, want one error naming the valid roles", msgs)
				}
				for _, valid := range []string{"viewer", "manager", "admin"} {
					if !strings.Contains(msgs[0].Text, valid) {
						t.Errorf("flash %q does not name the %q role", msgs[0].Text, valid)
					}
				}
			})
		}
	}
}

// TestLogout_RequiresCSRFToken is V10: logout now bumps the session epoch and
// ends every session the user holds, so a request that carries no valid token
// must not reach that code at all.
func TestLogout_RequiresCSRFToken(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // G101: test-only form bodies
		"no token":    "",
		"wrong token": "csrf_token=not-the-session-token",
		"empty token": "csrf_token=",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := testStore()
			cookies, _ := seedCSRF(t, store)
			h := newTestHandler(&config.Config{}, store)

			req := testReq("POST", "/logout", body)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rr := httptest.NewRecorder()
			// h.db is nil: reaching BumpSessionEpoch would panic.
			h.Logout(rr, req)

			if rr.Code != http.StatusSeeOther {
				t.Fatalf("expected 303 from the CSRF guard, got %d", rr.Code)
			}
			if loc := rr.Header().Get("Location"); loc != "/" {
				t.Errorf("Location: got %q want %q", loc, "/")
			}
		})
	}
}

// TestLogoutGet_DoesNotLogOut keeps an old bookmark degrading safely: the GET
// route redirects and, with a nil h.db, proves it never touches the user row.
func TestLogoutGet_DoesNotLogOut(t *testing.T) {
	store := testStore()
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"user_id":  1,
		"username": "alice",
	})
	h := newTestHandler(&config.Config{}, store)

	req := testReq("GET", "/logout", "")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.LogoutGet(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location: got %q want %q", loc, "/login")
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Errorf("GET /logout rewrote the session cookie: %+v", rr.Result().Cookies())
	}
}
