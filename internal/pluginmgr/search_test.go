package pluginmgr

import (
	"context"
	"errors"
	"testing"

	"go.ed.linuxfoundation.org/lfed/internal/config"
)

func TestSearchPropagatesCancellation(t *testing.T) {
	setPluginDir(t)
	f, _ := testFactory(t)
	f.Config = func() (*config.Config, error) {
		return &config.Config{Catalogs: map[string]config.Catalog{"broken": {Ref: "/does/not/exist.json"}}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Search(ctx, f, "deploy", Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
