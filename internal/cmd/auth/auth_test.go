package auth

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
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"go.ed.linuxfoundation.org/lfed/internal/authflow"
	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/internal/credstore"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

type fixture struct {
	f        *cmdutil.Factory
	out      *bytes.Buffer
	errOut   *bytes.Buffer
	stateDir string
	tokenReq int
}

type reply struct {
	status int
	body   map[string]any
}

func okToken(accessToken string) reply {
	return reply{status: http.StatusOK, body: map[string]any{"access_token": accessToken, "expires_in": 3600, "token_type": "Bearer"}}
}

func newFixture(t *testing.T, tokenReply reply) *fixture {
	t.Helper()
	keyring.MockInit()
	fx := &fixture{stateDir: t.TempDir()}
	t.Setenv("LFED_STATE_DIR", fx.stateDir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		fx.tokenReq++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenReply.status)
		json.NewEncoder(w).Encode(tokenReply.body)
	}))
	t.Cleanup(srv.Close)
	c := contexts.Context{Name: contexts.Staging, Issuer: srv.URL + "/", Audience: "https://api.staging.test/", ClientID: "client-123"}
	ios, _, out, errOut := iostreams.Test()
	fx.f = &cmdutil.Factory{
		IOStreams:  ios,
		OutputFlag: "table",
		Context:    func() (contexts.Context, error) { return c, nil },
	}
	fx.f.Token = cmdutil.TokenResolver(fx.f)
	fx.out, fx.errOut = out, errOut
	return fx
}

func (fx *fixture) seed(t *testing.T, accessToken string, insecure bool) {
	t.Helper()
	store, err := credstore.New(credstore.Options{Context: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(credstore.Credentials{RefreshToken: "rt", AccessToken: accessToken, AccessTokenExpiry: time.Now().Add(time.Hour)}, insecure); err != nil {
		t.Fatal(err)
	}
}

func (fx *fixture) run(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewCmd(fx.f)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	cmd.SetOut(fx.f.IOStreams.Out)
	cmd.SetErr(fx.f.IOStreams.ErrOut)
	return cmd.ExecuteContext(context.Background())
}

func (fx *fixture) credentialsFile() string {
	return filepath.Join(fx.stateDir, "credentials-staging.json")
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

func TestStatusNotLoggedIn(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	err := fx.run(t, "status")
	if err == nil || err.Error() != `not logged in; run "lfed auth login"` {
		t.Fatalf("err = %v", err)
	}
	if fx.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", fx.out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	fx.seed(t, jwt(t, map[string]any{authflow.UsernameClaim: "jdoe", "iss": "https://issuer.test/", "exp": exp.Unix()}), false)
	fx.f.OutputFlag = "json"
	if err := fx.run(t, "status"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Context  string    `json:"context"`
		Username string    `json:"username"`
		Issuer   string    `json:"issuer"`
		Storage  string    `json:"storage"`
		Expires  time.Time `json:"expires"`
	}
	if err := json.Unmarshal(fx.out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", fx.out.String(), err)
	}
	if got.Context != "staging" || got.Username != "jdoe" || got.Issuer != "https://issuer.test/" || got.Storage != "keyring" || !got.Expires.Equal(exp) {
		t.Errorf("status = %+v", got)
	}
}

func TestStatusTable(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	fx.seed(t, jwt(t, map[string]any{authflow.UsernameClaim: "jdoe", "exp": time.Now().Add(time.Hour).Unix()}), true)
	if err := fx.run(t, "status"); err != nil {
		t.Fatal(err)
	}
	out := fx.out.String()
	for _, want := range []string{"USERNAME", "jdoe", "staging", "STORAGE", "file", "ACCESS TOKEN EXPIRES"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestTokenPrintsOnlyToken(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	fx.seed(t, "opaque-token", true)
	if err := fx.run(t, "token"); err != nil {
		t.Fatal(err)
	}
	if fx.out.String() != "opaque-token\n" {
		t.Errorf("stdout = %q", fx.out.String())
	}
	if fx.errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", fx.errOut.String())
	}
}

func TestLoginWithTokenThenLogout(t *testing.T) {
	fx := newFixture(t, okToken("at-1"))
	fx.f.IOStreams.In = strings.NewReader("  my-refresh-token \n")
	if err := fx.run(t, "login", "--with-token"); err != nil {
		t.Fatal(err)
	}
	if fx.out.Len() != 0 || !strings.Contains(fx.errOut.String(), "Logged in to staging") {
		t.Errorf("stdout = %q, stderr = %q", fx.out.String(), fx.errOut.String())
	}
	if fx.tokenReq != 1 {
		t.Errorf("token requests = %d, want 1", fx.tokenReq)
	}
	store, err := credstore.New(credstore.Options{Context: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	creds, backend, err := store.LoadCredentials()
	if err != nil || backend != credstore.Keyring || creds.RefreshToken != "my-refresh-token" || creds.AccessToken != "at-1" {
		t.Fatalf("creds = %+v, %q, %v", creds, backend, err)
	}

	fx.errOut.Reset()
	if err := fx.run(t, "logout"); err != nil {
		t.Fatal(err)
	}
	if fx.errOut.String() != "Logged out of staging.\n" {
		t.Errorf("stderr = %q", fx.errOut.String())
	}
	if err := fx.run(t, "token"); err == nil || err.Error() != `not logged in; run "lfed auth login"` {
		t.Fatalf("token after logout: %v", err)
	}
}

func TestLoginWithTokenRejected(t *testing.T) {
	fx := newFixture(t, reply{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_grant", "error_description": "Unknown or invalid refresh token."}})
	fx.f.IOStreams.In = strings.NewReader("bad-token\n")
	err := fx.run(t, "login", "--with-token", "--insecure-storage")
	if err == nil || err.Error() != "refresh token rejected by the issuer" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(fx.credentialsFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credentials file after rejected login: %v", err)
	}
	if _, err := keyring.Get("lfed", "credentials/staging"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring item after rejected login: %v", err)
	}
}

func TestLoginWithTokenEmptyStdin(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	if err := fx.run(t, "login", "--with-token"); err == nil || !strings.Contains(err.Error(), "no refresh token provided") {
		t.Fatalf("err = %v", err)
	}
}

func TestInsecureStorageLoginAndStatus(t *testing.T) {
	fx := newFixture(t, okToken(jwt(t, map[string]any{authflow.UsernameClaim: "jdoe", "exp": time.Now().Add(time.Hour).Unix()})))
	fx.f.IOStreams.In = strings.NewReader("my-refresh-token\n")
	if err := fx.run(t, "login", "--with-token", "--insecure-storage"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fx.credentialsFile()); err != nil {
		t.Fatalf("credentials file after login: %v", err)
	}
	if _, err := keyring.Get("lfed", "credentials/staging"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring item after --insecure-storage login: %v", err)
	}
	if err := fx.run(t, "status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fx.out.String(), "file") {
		t.Errorf("status table lacks storage \"file\":\n%s", fx.out.String())
	}
}

func TestLogoutRemovesBothBackends(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	fx.seed(t, "at", true)
	if err := keyring.Set("lfed", "credentials/staging", `{"refresh_token":"rt-keyring"}`); err != nil {
		t.Fatal(err)
	}
	if err := fx.run(t, "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fx.credentialsFile()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credentials file after logout: %v", err)
	}
	if _, err := keyring.Get("lfed", "credentials/staging"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring item after logout: %v", err)
	}
}

func TestLoginRejectsRootStorageFlagElsewhere(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	err := fx.run(t, "token", "--insecure-storage")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --insecure-storage") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthUnknownSubcommand(t *testing.T) {
	fx := newFixture(t, okToken("at"))
	if err := fx.run(t, "nosuchcmd"); err == nil || !strings.Contains(err.Error(), `unknown command "nosuchcmd"`) {
		t.Fatalf("err = %v", err)
	}
}
