package credstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

func newTestStore(t *testing.T, dir, context string) Store {
	t.Helper()
	keyring.MockInit()
	store, err := New(Options{Context: context, StateDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func TestNewRequiresContext(t *testing.T) {
	if _, err := New(Options{StateDir: t.TempDir()}); err == nil {
		t.Fatal("New without Context: got nil error")
	}
}

func TestStateDirFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_STATE_DIR", dir)
	store, err := New(Options{Context: "staging"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.SaveCredentials(Credentials{RefreshToken: "rt"}, true); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials-staging.json")); err != nil {
		t.Fatalf("credentials file not in LFED_STATE_DIR: %v", err)
	}
}

func TestKeyringPerContext(t *testing.T) {
	dir := t.TempDir()
	staging := newTestStore(t, dir, "staging")
	prod := newTestStore(t, dir, "prod")
	if err := staging.DeleteCredentials(); err != nil {
		t.Fatalf("delete before save: %v", err)
	}
	if err := staging.SaveCredentials(Credentials{RefreshToken: "rt-staging"}, false); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	if _, _, err := prod.LoadCredentials(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prod sees staging credentials: %v", err)
	}
	got, backend, err := staging.LoadCredentials()
	if err != nil || got.RefreshToken != "rt-staging" || backend != Keyring {
		t.Fatalf("LoadCredentials = %+v, %q, %v", got, backend, err)
	}
	if err := staging.DeleteCredentials(); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}
	if _, _, err := staging.LoadCredentials(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestFileRoundTrip(t *testing.T) {
	store := newTestStore(t, t.TempDir(), "staging")

	if _, _, err := store.LoadCredentials(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadCredentials before save: got err %v, want ErrNotFound", err)
	}

	want := Credentials{
		RefreshToken:      "refresh-token",
		AccessToken:       "access-token",
		AccessTokenExpiry: time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := store.SaveCredentials(want, true); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	got, backend, err := store.LoadCredentials()
	if err != nil || backend != File {
		t.Fatalf("LoadCredentials: %q, %v", backend, err)
	}
	if got.RefreshToken != want.RefreshToken || got.AccessToken != want.AccessToken || !got.AccessTokenExpiry.Equal(want.AccessTokenExpiry) {
		t.Fatalf("LoadCredentials = %+v, want %+v", got, want)
	}

	if err := store.DeleteCredentials(); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadCredentials after delete: got err %v, want ErrNotFound", err)
	}
	if err := store.DeleteCredentials(); err != nil {
		t.Fatalf("DeleteCredentials on empty store: %v", err)
	}
}

func TestSaveSwitchesBackend(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir, "staging")
	path := filepath.Join(dir, "credentials-staging.json")

	if err := store.SaveCredentials(Credentials{RefreshToken: "rt-keyring"}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(Credentials{RefreshToken: "rt-file"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get(serviceName, "credentials/staging"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keyring item after switch to file: %v", err)
	}
	got, backend, err := store.LoadCredentials()
	if err != nil || backend != File || got.RefreshToken != "rt-file" {
		t.Fatalf("LoadCredentials = %+v, %q, %v", got, backend, err)
	}

	if err := store.SaveCredentials(Credentials{RefreshToken: "rt-keyring-2"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credentials file after switch to keyring: %v", err)
	}
	got, backend, err = store.LoadCredentials()
	if err != nil || backend != Keyring || got.RefreshToken != "rt-keyring-2" {
		t.Fatalf("LoadCredentials = %+v, %q, %v", got, backend, err)
	}
}

func TestLoadFallsBackToFileWithoutKeyring(t *testing.T) {
	store := newTestStore(t, t.TempDir(), "staging")
	if err := store.SaveCredentials(Credentials{RefreshToken: "rt-file"}, true); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("no D-Bus session"))
	t.Cleanup(keyring.MockInit)

	got, backend, err := store.LoadCredentials()
	if err != nil || backend != File || got.RefreshToken != "rt-file" {
		t.Fatalf("LoadCredentials = %+v, %q, %v", got, backend, err)
	}
	if err := store.DeleteCredentials(); err != nil {
		t.Fatalf("DeleteCredentials without keyring: %v", err)
	}
	if _, _, err := store.LoadCredentials(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestSaveToKeyringFailsWithoutKeyring(t *testing.T) {
	store := newTestStore(t, t.TempDir(), "staging")
	keyring.MockInitWithError(errors.New("no D-Bus session"))
	t.Cleanup(keyring.MockInit)
	err := store.SaveCredentials(Credentials{RefreshToken: "rt"}, false)
	if err == nil || !strings.Contains(err.Error(), "--insecure-storage") {
		t.Fatalf("err = %v", err)
	}
}

func TestCredentialsFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials-dev.json")

	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, dir, "dev")
	if err := store.SaveCredentials(Credentials{RefreshToken: "rt"}, true); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != want {
			t.Errorf("%s mode = %o, want %o", p, perm, want)
		}
	}
}

func TestWriteOwnerOnlyFileFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteOwnerOnlyFile(target, []byte("data")); err == nil {
		t.Fatal("WriteOwnerOnlyFile over a directory: got nil error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteOwnerOnlyFileReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	for _, data := range []string{"first, longer content", "second"} {
		if err := WriteOwnerOnlyFile(path, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "second" {
		t.Fatalf("content = %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dir entries = %v, %v", entries, err)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	first := newTestStore(t, dir, "dev")
	second := newTestStore(t, dir, "dev")
	unlock, err := first.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := second.Lock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock while held: err = %v, want context.Canceled", err)
	}

	unlockProd, err := newTestStore(t, dir, "prod").Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock prod: %v", err)
	}
	unlockProd()
	unlock()
	unlock2, err := second.Lock(context.Background())
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
	if _, err := os.Stat(filepath.Join(dir, "lock-dev")); err != nil {
		t.Errorf("lock file: %v", err)
	}
}

func TestContextIsolation(t *testing.T) {
	dir := t.TempDir()
	staging := newTestStore(t, dir, "staging")
	prod := newTestStore(t, dir, "prod")

	if err := prod.SaveCredentials(Credentials{RefreshToken: "prod-rt"}, true); err != nil {
		t.Fatalf("SaveCredentials prod: %v", err)
	}
	if err := staging.SaveCredentials(Credentials{RefreshToken: "staging-rt"}, true); err != nil {
		t.Fatalf("SaveCredentials staging: %v", err)
	}
	if err := staging.DeleteCredentials(); err != nil {
		t.Fatalf("DeleteCredentials staging: %v", err)
	}

	got, _, err := prod.LoadCredentials()
	if err != nil || got.RefreshToken != "prod-rt" {
		t.Fatalf("prod credentials after staging logout = %+v, %v; want prod-rt", got, err)
	}
}

func TestValidAccessToken(t *testing.T) {
	tests := []struct {
		name  string
		creds Credentials
		want  bool
	}{
		{
			name:  "no access token",
			creds: Credentials{AccessTokenExpiry: time.Now().Add(time.Hour)},
			want:  false,
		},
		{
			name:  "expired",
			creds: Credentials{AccessToken: "tok", AccessTokenExpiry: time.Now().Add(-time.Minute)},
			want:  false,
		},
		{
			name:  "expires within 60 seconds",
			creds: Credentials{AccessToken: "tok", AccessTokenExpiry: time.Now().Add(30 * time.Second)},
			want:  false,
		},
		{
			name:  "valid",
			creds: Credentials{AccessToken: "tok", AccessTokenExpiry: time.Now().Add(time.Hour)},
			want:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.creds.ValidAccessToken(); got != tc.want {
				t.Errorf("ValidAccessToken() = %v, want %v", got, tc.want)
			}
		})
	}
}
