package pluginmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"

	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

const ManifestFile = "lfed-plugin.json"

type Installed struct {
	Manifest sdkplugin.Manifest

	Source Source

	Dir string

	Binary string
}

type record struct {
	sdkplugin.Manifest
	Source Source `json:"source"`
}

type NotFoundError struct {
	Name        string
	Suggestions []string
}

func (e *NotFoundError) Error() string {
	if len(e.Suggestions) > 0 {
		return fmt.Sprintf("unknown command %q for \"lfed\"; did you mean %s?", e.Name, strings.Join(e.Suggestions, ", "))
	}
	return fmt.Sprintf("unknown command %q for \"lfed\"; run \"lfed plugin list\"", e.Name)
}

type IncompatibleError struct {
	Name     string
	Requires string
	Running  string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("plugin %q requires lfed %s; the lfed CLI is %s", e.Name, e.Requires, e.Running)
}

func Dir() (string, error) {
	if d := os.Getenv("LFED_PLUGIN_DIR"); d != "" {
		return d, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "lfed", "plugins"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve plugin dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "lfed", "plugins"), nil
}

func BinaryName(name string) string {
	return "lfed-" + name
}

func List() ([]Installed, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin dir: %w", err)
	}
	var out []Installed
	for _, e := range entries {
		if !e.IsDir() || !nameRe.MatchString(e.Name()) {
			continue
		}
		p, err := load(dir, e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, nil
}

func Lookup(name string) (Installed, error) {
	if !nameRe.MatchString(name) {
		return Installed{}, &NotFoundError{Name: name}
	}
	dir, err := Dir()
	if err != nil {
		return Installed{}, err
	}
	bin := filepath.Join(dir, name, BinaryName(name))
	if _, err := os.Stat(bin); err != nil {
		return Installed{}, &NotFoundError{Name: name}
	}
	p, err := load(dir, name)
	if errors.Is(err, os.ErrNotExist) {
		return Installed{}, fmt.Errorf("plugin %q has no %s; run \"lfed plugin install\" again", name, ManifestFile)
	}
	return p, err
}

func load(dir, name string) (Installed, error) {
	pdir := filepath.Join(dir, name)
	data, err := os.ReadFile(filepath.Join(pdir, ManifestFile))
	if err != nil {
		return Installed{}, err
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return Installed{}, fmt.Errorf("parse %s: %w", filepath.Join(pdir, ManifestFile), err)
	}
	if r.SchemaVersion != sdkplugin.ManifestSchemaVersion {
		return Installed{}, fmt.Errorf("plugin %q has manifest schema_version %q; this lfed reads %q; run \"lfed plugin install\" again",
			name, r.SchemaVersion, sdkplugin.ManifestSchemaVersion)
	}
	return Installed{Manifest: r.Manifest, Source: r.Source, Dir: pdir, Binary: filepath.Join(pdir, BinaryName(name))}, nil
}

func CheckLfedVersion(requires, running string) error {
	c, err := semver.NewConstraint(requires)
	if err != nil {
		return fmt.Errorf("invalid requires_lfed %q: %w", requires, err)
	}
	v, err := semver.NewVersion(running)
	if err != nil {
		return nil
	}
	release, err := v.SetPrerelease("")
	if err != nil {
		return err
	}
	if !c.Check(&release) {
		return &IncompatibleError{Requires: requires, Running: running}
	}
	return nil
}
