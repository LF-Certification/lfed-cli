package jq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

func Evaluate(ctx context.Context, w io.Writer, expr string, doc []byte) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return fmt.Errorf("--jq %q: %w", expr, err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return fmt.Errorf("--jq %q: %w", expr, err)
	}
	var input any
	if err := json.Unmarshal(doc, &input); err != nil {
		return fmt.Errorf("--jq needs JSON output but the command printed something else: %w", err)
	}
	iter := code.RunWithContext(ctx, input)
	for {
		v, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := v.(error); isErr {
			return fmt.Errorf("--jq: %w", err)
		}
		if s, isStr := v.(string); isStr {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return err
			}
			continue
		}
		b, err := gojq.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, string(b)); err != nil {
			return err
		}
	}
}
