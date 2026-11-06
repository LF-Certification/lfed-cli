package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"go.ed.linuxfoundation.org/lfed/internal/credstore"
)

const fileName = "config.yaml"

type Config struct {
	CurrentContext string `yaml:"current_context,omitempty"`

	// Catalogs maps a catalog alias to its location and signer. The alias
	// "default" overrides the built-in catalog that a bare plugin name
	// resolves through. Any other alias resolves <alias>/<name>.
	Catalogs map[string]Catalog `yaml:"catalogs,omitempty"`
}

// Catalog is one plugin catalog: its location and the identity that
// MUST have signed it. Both signer fields MUST be set together. A file
// path needs no signer.
type Catalog struct {
	Ref    string        `yaml:"ref,omitempty"`
	Signer CatalogSigner `yaml:"signer,omitempty"`
}

type CatalogSigner struct {
	Issuer   string `yaml:"issuer,omitempty"`
	Identity string `yaml:"identity,omitempty"`
}

func (s CatalogSigner) IsZero() bool { return s.Issuer == "" && s.Identity == "" }

func Dir() (string, error) {
	if d := os.Getenv("LFED_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "lfed"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(home, ".config", "lfed"), nil
}

func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(dir, fileName), err)
	}
	return &cfg, nil
}

func (c *Config) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := credstore.MkdirPrivate(dir); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := credstore.WriteOwnerOnlyFile(filepath.Join(dir, fileName), data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
