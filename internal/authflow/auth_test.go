package authflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/internal/credstore"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

type tokenReply struct {
	status int
	body   map[string]any
}

func oauthErr(code string) tokenReply {
	return tokenReply{status: http.StatusBadRequest, body: map[string]any{"error": code, "error_description": code}}
}

func oauthOK(body map[string]any) tokenReply {
	return tokenReply{status: http.StatusOK, body: body}
}

type fakeAuth0 struct {
	t       *testing.T
	mu      sync.Mutex
	replies []tokenReply

	deviceReqs []map[string]string
	tokenReqs  []map[string]string
	expiresIn  int
}

func newFakeAuth0(t *testing.T, replies ...tokenReply) (*fakeAuth0, contexts.Context) {
	t.Helper()
	f := &fakeAuth0{t: t, replies: replies, expiresIn: 60}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, contexts.Context{
		Name:     contexts.Staging,
		Issuer:   srv.URL + "/",
		Audience: "https://api.staging.test/",
		ClientID: "client-123",
	}
}

func (f *fakeAuth0) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("parse form: %v", err)
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/oauth/device/code":
		f.deviceReqs = append(f.deviceReqs, form)
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-code",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          "https://example.test/activate",
			"verification_uri_complete": "https://example.test/activate?user_code=ABCD-EFGH",
			"expires_in":                f.expiresIn,
			"interval":                  1,
		})
	case "/oauth/token":
		f.tokenReqs = append(f.tokenReqs, form)
		reply := f.replies[0]
		if len(f.replies) > 1 {
			f.replies = f.replies[1:]
		}
		w.WriteHeader(reply.status)
		json.NewEncoder(w).Encode(reply.body)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAuth0) tokenRequests() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.tokenReqs...)
}

func jwt(t *testing.T, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	seg := base64.RawURLEncoding.EncodeToString
	return seg([]byte(`{"alg":"none"}`)) + "." + seg(b) + ".sig"
}

func newStore(t *testing.T, dir string) credstore.Store {
	t.Helper()
	keyring.MockInit()
	store, err := credstore.New(credstore.Options{Context: "staging", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testIO() (*iostreams.IOStreams, *bytes.Buffer) {
	ios, _, _, errOut := iostreams.Test()
	return ios, errOut
}

func seed(t *testing.T, store credstore.Store, creds credstore.Credentials) {
	t.Helper()
	if err := store.SaveCredentials(creds, true); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLoginPendingThenSuccess(t *testing.T) {
	fake, c := newFakeAuth0(t,
		oauthErr("authorization_pending"),
		oauthOK(map[string]any{
			"access_token":  "at-1",
			"refresh_token": "rt-1",
			"expires_in":    3600,
			"token_type":    "Bearer",
			"id_token":      jwt(t, map[string]any{idTokenUsernameClaim: "jdoe", "email": "jdoe@example.test"}),
		}),
	)
	store := newStore(t, t.TempDir())
	ios, out := testIO()

	identity, err := DeviceLogin(context.Background(), c, store, LoginOptions{IO: ios, OpenBrowser: true, Insecure: true})
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if identity != "jdoe (jdoe@example.test)" {
		t.Errorf("identity = %q", identity)
	}
	if !strings.Contains(out.String(), "! First copy your one-time code: ABCD-EFGH") || !strings.Contains(out.String(), "Then visit: https://example.test/activate?user_code=ABCD-EFGH") {
		t.Errorf("non-TTY instructions missing code or URL:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Press Enter") {
		t.Errorf("non-TTY login prompted for Enter:\n%s", out.String())
	}

	dev := fake.deviceReqs[0]
	if dev["audience"] != c.Audience || dev["client_id"] != c.ClientID || !strings.Contains(dev["scope"], "offline_access") {
		t.Errorf("device request = %v", dev)
	}
	reqs := fake.tokenRequests()
	if len(reqs) != 2 {
		t.Fatalf("token requests = %d, want 2", len(reqs))
	}
	if reqs[0]["grant_type"] != "urn:ietf:params:oauth:grant-type:device_code" || reqs[0]["device_code"] != "dev-code" || reqs[0]["client_id"] != c.ClientID {
		t.Errorf("token request = %v", reqs[0])
	}

	creds, backend, err := store.LoadCredentials()
	if err != nil || backend != credstore.File {
		t.Fatalf("LoadCredentials: %q, %v", backend, err)
	}
	if creds.RefreshToken != "rt-1" || creds.AccessToken != "at-1" || !creds.ValidAccessToken() {
		t.Errorf("persisted creds = %+v", creds)
	}
}

func TestDeviceLoginTTYWaitsForEnter(t *testing.T) {
	const prompt = "Press Enter to open https://example.test/activate?user_code=ABCD-EFGH in your browser..."
	for name, tc := range map[string]struct {
		browser string
		want    string
	}{
		"opens":  {browser: "true", want: "Waiting for authentication..."},
		"failed": {browser: filepath.Join(t.TempDir(), "missing"), want: "Failed opening a web browser at https://example.test/activate?user_code=ABCD-EFGH"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BROWSER", tc.browser)
			_, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "token_type": "Bearer"}))
			ios, out := testIO()
			ios.SetStdinTTY(true)
			ios.In = strings.NewReader("\n")
			if _, err := DeviceLogin(context.Background(), c, newStore(t, t.TempDir()), LoginOptions{IO: ios, OpenBrowser: true}); err != nil {
				t.Fatalf("DeviceLogin: %v", err)
			}
			if !strings.Contains(out.String(), prompt) || !strings.Contains(out.String(), tc.want) {
				t.Errorf("output:\n%s", out.String())
			}
		})
	}
}

func TestDeviceLoginNoBrowserOnTTY(t *testing.T) {
	_, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "token_type": "Bearer"}))
	ios, out := testIO()
	ios.SetStdinTTY(true)
	if _, err := DeviceLogin(context.Background(), c, newStore(t, t.TempDir()), LoginOptions{IO: ios, OpenBrowser: false, Insecure: true}); err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if strings.Contains(out.String(), "Press Enter") || !strings.Contains(out.String(), "Then visit: https://example.test/activate?user_code=ABCD-EFGH") {
		t.Errorf("--no-browser output:\n%s", out.String())
	}
}

func TestDeviceLoginSlowDown(t *testing.T) {
	if testing.Short() {
		t.Skip("slow_down back-off takes several seconds")
	}
	fake, c := newFakeAuth0(t,
		oauthErr("slow_down"),
		oauthOK(map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "token_type": "Bearer"}),
	)
	ios, _ := testIO()
	start := time.Now()
	if _, err := DeviceLogin(context.Background(), c, newStore(t, t.TempDir()), LoginOptions{IO: ios, Insecure: true}); err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 6*time.Second {
		t.Errorf("polling did not back off after slow_down: %v", elapsed)
	}
	if n := len(fake.tokenRequests()); n != 2 {
		t.Errorf("token requests = %d, want 2", n)
	}
}

func TestDeviceLoginExpired(t *testing.T) {
	_, c := newFakeAuth0(t, oauthErr("expired_token"))
	store := newStore(t, t.TempDir())
	ios, _ := testIO()
	_, err := DeviceLogin(context.Background(), c, store, LoginOptions{IO: ios, Insecure: true})
	if err == nil || err.Error() != "timed out waiting for login" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("credentials stored after failed login: %v", err)
	}
}

func TestDeviceLoginDenied(t *testing.T) {
	_, c := newFakeAuth0(t, oauthErr("access_denied"))
	ios, _ := testIO()
	_, err := DeviceLogin(context.Background(), c, newStore(t, t.TempDir()), LoginOptions{IO: ios, Insecure: true})
	if err == nil || err.Error() != "login was denied" {
		t.Fatalf("err = %v", err)
	}
}

func TestDeviceLoginWithoutRefreshToken(t *testing.T) {
	_, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at", "expires_in": 3600, "token_type": "Bearer"}))
	store := newStore(t, t.TempDir())
	ios, _ := testIO()
	_, err := DeviceLogin(context.Background(), c, store, LoginOptions{IO: ios, Insecure: true})
	if err == nil || !strings.Contains(err.Error(), "did not return a refresh token") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("credentials stored without refresh token: %v", err)
	}
}

func TestTokenLoginValidatesAndStores(t *testing.T) {
	fake, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at-1", "expires_in": 3600, "token_type": "Bearer"}))
	store := newStore(t, t.TempDir())
	if err := TokenLogin(context.Background(), c, store, " rt-supplied \n", false); err != nil {
		t.Fatalf("TokenLogin: %v", err)
	}
	reqs := fake.tokenRequests()
	if len(reqs) != 1 || reqs[0]["grant_type"] != "refresh_token" || reqs[0]["refresh_token"] != "rt-supplied" {
		t.Errorf("token requests = %v", reqs)
	}
	creds, backend, err := store.LoadCredentials()
	if err != nil || backend != credstore.Keyring {
		t.Fatalf("LoadCredentials: %q, %v", backend, err)
	}
	if creds.RefreshToken != "rt-supplied" || creds.AccessToken != "at-1" || !creds.ValidAccessToken() {
		t.Errorf("persisted creds = %+v", creds)
	}
}

func TestTokenLoginRejectedStoresNothing(t *testing.T) {
	_, c := newFakeAuth0(t, oauthErr("invalid_grant"))
	store := newStore(t, t.TempDir())
	err := TokenLogin(context.Background(), c, store, "bad", true)
	if err == nil || err.Error() != "refresh token rejected by the issuer" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("credentials after rejected login: %v", err)
	}
}

func TestTokenLoginEmpty(t *testing.T) {
	fake, c := newFakeAuth0(t, oauthErr("server_error"))
	err := TokenLogin(context.Background(), c, newStore(t, t.TempDir()), " \n", true)
	if err == nil || !strings.Contains(err.Error(), "no refresh token provided") {
		t.Fatalf("err = %v", err)
	}
	if n := len(fake.tokenRequests()); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
}

func TestTokenCachedNoNetwork(t *testing.T) {
	fake, c := newFakeAuth0(t, oauthErr("server_error"))
	store := newStore(t, t.TempDir())
	seed(t, store, credstore.Credentials{RefreshToken: "rt", AccessToken: "cached", AccessTokenExpiry: time.Now().Add(time.Hour)})
	got, err := Token(context.Background(), c, store)
	if err != nil || got != "cached" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	if n := len(fake.tokenRequests()); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
}

func TestTokenRefreshesExpired(t *testing.T) {
	fake, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at-2", "refresh_token": "rt-2", "expires_in": 3600, "token_type": "Bearer"}))
	store := newStore(t, t.TempDir())
	seed(t, store, credstore.Credentials{RefreshToken: "rt-1", AccessToken: "at-1", AccessTokenExpiry: time.Now().Add(30 * time.Second)})
	got, err := Token(context.Background(), c, store)
	if err != nil || got != "at-2" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	reqs := fake.tokenRequests()
	if len(reqs) != 1 || reqs[0]["grant_type"] != "refresh_token" || reqs[0]["refresh_token"] != "rt-1" || reqs[0]["client_id"] != c.ClientID {
		t.Errorf("refresh request = %v", reqs)
	}
	creds, backend, err := store.LoadCredentials()
	if err != nil || backend != credstore.File {
		t.Fatalf("LoadCredentials: %q, %v", backend, err)
	}
	if creds.AccessToken != "at-2" || creds.RefreshToken != "rt-2" || !creds.ValidAccessToken() {
		t.Errorf("persisted creds = %+v", creds)
	}
}

func TestTokenRefreshKeepsBackendAndRefreshToken(t *testing.T) {
	_, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at-2", "expires_in": 3600, "token_type": "Bearer"}))
	store := newStore(t, t.TempDir())
	if err := store.SaveCredentials(credstore.Credentials{RefreshToken: "rt-1"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Token(context.Background(), c, store); err != nil {
		t.Fatal(err)
	}
	creds, backend, err := store.LoadCredentials()
	if err != nil || backend != credstore.Keyring || creds.RefreshToken != "rt-1" {
		t.Errorf("after refresh = %+v, %q, %v; want rt-1 in keyring", creds, backend, err)
	}
}

func TestTokenErrors(t *testing.T) {
	_, c := newFakeAuth0(t, oauthErr("invalid_grant"))

	t.Run("not logged in", func(t *testing.T) {
		_, err := Token(context.Background(), c, newStore(t, t.TempDir()))
		if !errors.Is(err, ErrNotLoggedIn) || err.Error() != `not logged in; run "lfed auth login"` {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid_grant", func(t *testing.T) {
		store := newStore(t, t.TempDir())
		seed(t, store, credstore.Credentials{RefreshToken: "revoked"})
		_, err := Token(context.Background(), c, store)
		if !errors.Is(err, ErrSessionExpired) || err.Error() != `session expired or revoked; run "lfed auth login"` {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no refresh token", func(t *testing.T) {
		store := newStore(t, t.TempDir())
		seed(t, store, credstore.Credentials{AccessToken: "stale", AccessTokenExpiry: time.Now().Add(-time.Hour)})
		if _, err := Token(context.Background(), c, store); !errors.Is(err, ErrNoRefreshToken) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLogoutRemovesBothBackends(t *testing.T) {
	dir := t.TempDir()
	store := newStore(t, dir)
	seed(t, store, credstore.Credentials{RefreshToken: "rt-file"})
	if err := keyring.Set("lfed", "credentials/staging", `{"refresh_token":"rt-keyring"}`); err != nil {
		t.Fatal(err)
	}
	if err := Logout(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials-staging.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credentials file after logout: %v", err)
	}
	if _, err := keyring.Get("lfed", "credentials/staging"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring item after logout: %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, credstore.ErrNotFound) {
		t.Errorf("credentials after logout: %v", err)
	}
	if err := Logout(context.Background(), store); err != nil {
		t.Errorf("second logout: %v", err)
	}
}

func TestTokenConcurrentRefreshOnce(t *testing.T) {
	fake, c := newFakeAuth0(t, oauthOK(map[string]any{"access_token": "at-2", "refresh_token": "rt-2", "expires_in": 3600, "token_type": "Bearer"}))
	dir := t.TempDir()
	seed(t, newStore(t, dir), credstore.Credentials{RefreshToken: "rt-1"})
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		store := newStore(t, dir)
		wg.Go(func() { tokens[i], errs[i] = Token(context.Background(), c, store) })
	}
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil || tokens[i] != "at-2" {
			t.Errorf("Token %d = %q, %v", i, tokens[i], errs[i])
		}
	}
	if n := len(fake.tokenRequests()); n != 1 {
		t.Errorf("refresh requests = %d, want 1", n)
	}
}

func TestLockWaitEndsWithContext(t *testing.T) {
	dir := t.TempDir()
	seed(t, newStore(t, dir), credstore.Credentials{RefreshToken: "rt"})
	unlock, err := newStore(t, dir).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Token(ctx, contexts.Context{}, newStore(t, dir)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Token with a held lock and a canceled context: err = %v", err)
	}
}

func TestDecodeClaims(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	tok := jwt(t, map[string]any{UsernameClaim: "jdoe", "iss": "https://issuer.test/", "sub": "auth0|1", "exp": exp.Unix()})
	claims, err := DecodeClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Username != "jdoe" || claims.Issuer != "https://issuer.test/" || claims.Subject != "auth0|1" || !claims.Expiry.Equal(exp) {
		t.Errorf("claims = %+v", claims)
	}
	if _, err := DecodeClaims("opaque"); err == nil {
		t.Error("DecodeClaims(opaque): want error")
	}
}

func TestTerminalSafeText(t *testing.T) {
	if got := terminalSafeText("ABCD-EFGH"); got != "ABCD-EFGH" {
		t.Errorf("plain = %q", got)
	}
	if got := terminalSafeText("A\x1b[31mB"); got != `"A\x1b[31mB"` {
		t.Errorf("escaped = %q", got)
	}
}

func TestValidateBrowserURL(t *testing.T) {
	for _, bad := range []string{"http://example.test", "file:///etc/passwd", "https://", "mailto:x@y", "https://example.test/\x07"} {
		if err := validateBrowserURL(bad); err == nil {
			t.Errorf("validateBrowserURL(%q): want error", bad)
		}
	}
	if err := validateBrowserURL("https://example.test/activate?user_code=X"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
}
