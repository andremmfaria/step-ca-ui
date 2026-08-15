package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	humaapi "step-ui/api"
	"step-ui/config"
	"step-ui/handlers"
	"step-ui/models"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/sessions"
)

// Mirrors the registration main.go does before store.Save can encode a
// session value through an interface{} map — tests never call main().
func init() {
	gob.Register(int(0))
	gob.Register(int64(0))
	gob.Register("")
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	hashKey := sha256.Sum256([]byte("phase0-spike-test-key-not-for-prod!"))
	blockKey := sha256.Sum256([]byte("phase0-spike-test-key-not-for-prod!_block"))
	store := sessions.NewCookieStore(hashKey[:], blockKey[:16])
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   28800,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // httptest.NewServer is plain HTTP
	}

	h := handlers.NewWithFS(nil, &config.Config{}, store, nil)
	r := chi.NewRouter()

	// A test-only login route. The handler is built with a nil database, so
	// the real login path is unreachable; this mints the same session values
	// middleware.ValidateSession reads and lets the cookie jar carry them.
	r.Post("/test-login", func(w http.ResponseWriter, req *http.Request) {
		sess, _ := store.Get(req, h.SessionCookieName())
		now := time.Now().Unix()
		sess.Values["user_id"] = testUser.ID
		sess.Values["session_epoch"] = testUser.SessionEpoch
		sess.Values["session_created_at"] = now
		sess.Values["last_activity"] = now
		sess.Values["csrf_token"] = testCSRFToken
		if err := sess.Save(req, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "step-ui-csrf", Value: testCSRFToken, Path: "/"}) //nolint:gosec // G124: the readable CSRF cookie is deliberately not HttpOnly (5.4), and this test server is plain HTTP
	})

	humaapi.Mount(r, h, stubUserLoader)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// testUser is the row stubUserLoader returns. Role manager satisfies both the
// viewer and the manager operations registered today.
var testUser = &models.User{ID: 1, Username: "spike", Role: "manager", IsActive: true, SessionEpoch: 7}

// testCSRFToken is the value the test-only login route puts in both halves of
// the session-bound pair (5.4).
const testCSRFToken = "test-csrf-token-value" //nolint:gosec // G101: a fixed value in a test, not a credential

// stubUserLoader stands in for db.GetUserByID, which cannot run against the
// nil database these tests build the handler with.
func stubUserLoader(id int) (*models.User, error) {
	if id != testUser.ID {
		return nil, nil
	}
	return testUser, nil
}

// login mints an authenticated session into client's cookie jar and returns
// the CSRF token to echo in X-CSRF-Token on unsafe methods.
func login(t *testing.T, client *http.Client, srv *httptest.Server) string {
	t.Helper()
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/test-login", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test-login: status = %d, want 200", resp.StatusCode)
	}
	return testCSRFToken
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{Jar: jar}
}

func doRequest(t *testing.T, client *http.Client, method, url string, body io.Reader, setHeaders func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatalf("NewRequestWithContext %s %s: %v", method, url, err)
	}
	if setHeaders != nil {
		setHeaders(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

type getResult struct {
	status  int
	body    map[string]any
	cookies []*http.Cookie
}

func doGet(t *testing.T, client *http.Client, url string) getResult {
	t.Helper()
	resp := doRequest(t, client, http.MethodGet, url, nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body from %s: %v", url, err)
	}
	return getResult{status: resp.StatusCode, body: body, cookies: resp.Cookies()}
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// TestSession_AnonymousRoundTrip is the go/no-go proof for the unwrap
// mechanic (D2 cost 2): getSession writes a CSRF token into the encrypted
// session from inside a huma operation handler, and this asserts a second
// call reads the same value back — only possible if store.Get inside the
// handler actually decrypted what store.Save wrote there.
func TestSession_AnonymousRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	first := doGet(t, client, srv.URL+"/api/v1/session")
	if first.status != http.StatusOK {
		t.Fatalf("first call: status = %d, want 200", first.status)
	}
	if first.body["state"] != "anonymous" {
		t.Fatalf("first call: state = %v, want anonymous", first.body["state"])
	}
	firstCSRF := cookieValue(first.cookies, "step-ui-csrf")
	if firstCSRF == "" {
		t.Fatal("first call: step-ui-csrf cookie not set")
	}
	if cookieValue(first.cookies, "step-ui") == "" { // plain HTTP test store, so no __Host- prefix (D6)
		t.Fatal("first call: step-ui session cookie not set")
	}

	second := doGet(t, client, srv.URL+"/api/v1/session")
	if second.status != http.StatusOK {
		t.Fatalf("second call: status = %d, want 200", second.status)
	}
	if second.body["state"] != "anonymous" {
		t.Fatalf("second call: state = %v, want anonymous", second.body["state"])
	}
	secondCSRF := cookieValue(second.cookies, "step-ui-csrf")
	if secondCSRF != firstCSRF {
		t.Fatalf("csrf token changed across requests: first=%q second=%q — session write did not round-trip", firstCSRF, secondCSRF)
	}
}

func TestStatus_RequiresSession(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/status", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
}

func TestConfig_IsPublic(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	result := doGet(t, client, srv.URL+"/api/v1/config")
	if result.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", result.status)
	}
	for _, key := range []string{
		"oidcEnabled", "oidcButtonLabel", "acmeEnabled", "appVersion",
		"contractSha", "roleLevels", "sessionIdleTimeoutSeconds", "expiringSoonDays",
	} {
		if _, ok := result.body[key]; !ok {
			t.Errorf("config response missing %q", key)
		}
	}
}

func TestSpikeBlob_Downloads(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/_spike/blob", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "step-ca-ui phase 0 spike blob\n" {
		t.Fatalf("body = %q", body)
	}
}

func multipartUploadBody(t *testing.T, includeFile bool) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if includeFile {
		fw, err := mw.CreateFormFile("file", "spike.bin")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write([]byte("hello")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func TestSpikeUpload_Succeeds(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	token := login(t, client, srv)

	buf, contentType := multipartUploadBody(t, true)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", buf, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("X-CSRF-Token", token)
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["filename"] != "spike.bin" {
		t.Fatalf("filename = %v, want spike.bin", body["filename"])
	}
	if body["sizeBytes"] != float64(5) {
		t.Fatalf("sizeBytes = %v, want 5", body["sizeBytes"])
	}
}

// TestSpikeUpload_MissingFileIsUnprocessableEntity proves huma's schema
// validation answers 422, not 400 (5.2).
func TestSpikeUpload_MissingFileIsUnprocessableEntity(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	token := login(t, client, srv)
	buf, contentType := multipartUploadBody(t, false)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", buf, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("X-CSRF-Token", token)
	})

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
}

func TestUnmatchedAPIPath_ReturnsProblemDocument(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/does-not-exist", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (an HTML 404 is what this guards against)", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
}

// TestDocsAndSpecEndpoint_NotRegistered is D9's check: DefaultConfig's
// auto-registered /docs and /openapi.json are wired below api.UseMiddleware
// and so bypass every authorisation check, which is why Mount clears
// DocsPath, OpenAPIPath and SchemasPath.
func TestDocsAndSpecEndpoint_NotRegistered(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	for _, path := range []string{"/docs", "/openapi.json", "/openapi.yaml", "/schemas/Session.json"} {
		resp := doRequest(t, client, http.MethodGet, srv.URL+path, nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, resp.StatusCode)
		}
	}
}
