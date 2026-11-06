package pluginmgr

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestCredentialScopesGitHubTokenToDefaultRegistry(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "gh-secret")
	t.Setenv("GH_TOKEN", "")
	ctx := context.Background()

	for _, host := range []string{"registry.example.com", "127.0.0.1:5000", "localhost:5000", "ghcr.io.example.com"} {
		got, err := credential(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		if got != auth.EmptyCredential {
			t.Errorf("credential(%q) = %+v, want anonymous", host, got)
		}
	}

	got, err := credential(ctx, defaultRegistryHost)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "gh-secret" {
		t.Errorf("credential(%q) = %+v, want the GITHUB_TOKEN", defaultRegistryHost, got)
	}
}
