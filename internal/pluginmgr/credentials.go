package pluginmgr

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// defaultRegistryHost is the registry that GITHUB_TOKEN and "gh auth
// token" are valid for. Other hosts use the Docker credential store only.
const defaultRegistryHost = "ghcr.io"

func credential(ctx context.Context, hostport string) (auth.Credential, error) {
	if c, ok := dockerCredential(ctx, hostport); ok {
		return c, nil
	}
	if hostport != defaultRegistryHost {
		return auth.EmptyCredential, nil
	}
	if tok := tokenFromEnv(); tok != "" {
		return tokenCredential(tok), nil
	}
	if tok := ghToken(ctx); tok != "" {
		return tokenCredential(tok), nil
	}
	return auth.EmptyCredential, nil
}

func dockerCredential(ctx context.Context, hostport string) (auth.Credential, bool) {
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return auth.EmptyCredential, false
	}
	c, err := store.Get(ctx, hostport)
	if err != nil || c == auth.EmptyCredential {
		return auth.EmptyCredential, false
	}
	return c, true
}

func tokenFromEnv() string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func ghToken(ctx context.Context) string {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	out, err := exec.CommandContext(ctx, gh, "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func tokenCredential(tok string) auth.Credential {
	return auth.Credential{Username: "x-access-token", Password: tok}
}
