package cmdutil

import (
	"context"

	"go.ed.linuxfoundation.org/lfed/internal/authflow"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/internal/credstore"
)

func OpenStore(f *Factory) (contexts.Context, credstore.Store, error) {
	c, err := f.Context()
	if err != nil {
		return contexts.Context{}, nil, err
	}
	store, err := credstore.New(credstore.Options{Context: string(c.Name)})
	if err != nil {
		return contexts.Context{}, nil, err
	}
	return c, store, nil
}

func TokenResolver(f *Factory) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		c, store, err := OpenStore(f)
		if err != nil {
			return "", err
		}
		return authflow.Token(ctx, c, store)
	}
}
