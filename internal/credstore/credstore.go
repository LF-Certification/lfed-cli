package credstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

var ErrNotFound = errors.New("credstore: not found")

const serviceName = "lfed"

type Credentials struct {
	RefreshToken      string    `json:"refresh_token"`
	AccessToken       string    `json:"access_token,omitempty"`
	AccessTokenExpiry time.Time `json:"access_token_expiry,omitzero"`
}

const refreshLeeway = 60 * time.Second

func (c Credentials) ValidAccessToken() bool {
	return c.AccessToken != "" && time.Now().Add(refreshLeeway).Before(c.AccessTokenExpiry)
}

type Backend string

const (
	Keyring Backend = "keyring"
	File    Backend = "file"
)

type Store interface {
	SaveCredentials(creds Credentials, insecure bool) error

	LoadCredentials() (Credentials, Backend, error)

	DeleteCredentials() error

	Lock(ctx context.Context) (unlock func(), err error)
}

const LockTimeout = 30 * time.Second

const lockRetry = 100 * time.Millisecond

type Options struct {
	Context string

	StateDir string
}

type store struct {
	keyringUser string
	filePath    string
	lockPath    string
}

func New(opts Options) (Store, error) {
	if opts.Context == "" {
		return nil, errors.New("credstore: context is required")
	}
	stateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return nil, fmt.Errorf("credstore: resolve state dir: %w", err)
	}
	return &store{
		keyringUser: "credentials/" + opts.Context,
		filePath:    filepath.Join(stateDir, "credentials-"+opts.Context+".json"),
		lockPath:    filepath.Join(stateDir, "lock-"+opts.Context),
	}, nil
}

func resolveStateDir(override string) (string, error) {
	dir := override
	if dir == "" {
		dir = os.Getenv("LFED_STATE_DIR")
	}
	if dir == "" {
		if xdgState := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(xdgState) {
			dir = filepath.Join(xdgState, "lfed")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("determine home directory: %w", err)
			}
			dir = filepath.Join(home, ".local", "state", "lfed")
		}
	}
	if err := MkdirPrivate(dir); err != nil {
		return "", fmt.Errorf("create state directory %q: %w", dir, err)
	}
	return dir, nil
}

func MkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(dir) // #nosec G703
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700) // #nosec G302,G703
	}
	return nil
}

func WriteOwnerOnlyFile(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *store) SaveCredentials(creds Credentials, insecure bool) error {
	if insecure {
		if err := s.saveFile(creds); err != nil {
			return err
		}
		s.deleteKeyring()
		return nil
	}
	if err := s.saveKeyring(creds); err != nil {
		return err
	}
	return s.deleteFile()
}

// A keyring that cannot be reached counts as empty, so headless hosts fall back to the file.
func (s *store) LoadCredentials() (Credentials, Backend, error) {
	data, err := keyring.Get(serviceName, s.keyringUser)
	if err == nil {
		creds, err := decode([]byte(data))
		return creds, Keyring, err
	}
	raw, err := os.ReadFile(s.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, "", ErrNotFound
	}
	if err != nil {
		return Credentials{}, "", fmt.Errorf("credstore: read credentials: %w", err)
	}
	creds, err := decode(raw)
	return creds, File, err
}

func (s *store) DeleteCredentials() error {
	s.deleteKeyring()
	return s.deleteFile()
}

func (s *store) Lock(ctx context.Context) (func(), error) {
	waitCtx, cancel := context.WithTimeout(ctx, LockTimeout)
	defer cancel()
	lock := flock.New(s.lockPath)
	locked, err := lock.TryLockContext(waitCtx, lockRetry)
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, fmt.Errorf("credstore: lock %s: %w", s.lockPath, ctx.Err())
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("credstore: another lfed process holds %s; waited %s", s.lockPath, LockTimeout)
	case err != nil:
		return nil, fmt.Errorf("credstore: lock %s: %w", s.lockPath, err)
	case !locked:
		return nil, fmt.Errorf("credstore: lock %s: not acquired", s.lockPath)
	}
	return func() { _ = lock.Unlock() }, nil
}

func (s *store) saveKeyring(creds Credentials) error {
	data, err := json.Marshal(creds) // #nosec G117
	if err != nil {
		return fmt.Errorf("credstore: marshal credentials: %w", err)
	}
	if err := keyring.Set(serviceName, s.keyringUser, string(data)); err != nil {
		return fmt.Errorf("credstore: save credentials (use --insecure-storage as a fallback): %w", err)
	}
	return nil
}

func (s *store) deleteKeyring() {
	_ = keyring.Delete(serviceName, s.keyringUser)
}

func (s *store) saveFile(creds Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ") // #nosec G117
	if err != nil {
		return fmt.Errorf("credstore: marshal credentials: %w", err)
	}
	if err := WriteOwnerOnlyFile(s.filePath, data); err != nil {
		return fmt.Errorf("credstore: write credentials: %w", err)
	}
	return nil
}

func (s *store) deleteFile() error {
	err := os.Remove(s.filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("credstore: delete credentials: %w", err)
	}
	return nil
}

func decode(data []byte) (Credentials, error) {
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("credstore: unmarshal credentials: %w", err)
	}
	return creds, nil
}
