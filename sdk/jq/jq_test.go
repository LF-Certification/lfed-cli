package jq

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFilterPrintsStringsRawAndValuesAsJSON(t *testing.T) {
	doc := []byte(`[{"name":"dev","n":1},{"name":"prod","n":2}]`)
	var out bytes.Buffer
	if err := Evaluate(context.Background(), &out, ".[].name", doc); err != nil {
		t.Fatal(err)
	}
	if out.String() != "dev\nprod\n" {
		t.Fatalf("got %q", out.String())
	}
	out.Reset()
	if err := Evaluate(context.Background(), &out, "map(.n)", doc); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[1,2]\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestFilterErrorsNameTheCause(t *testing.T) {
	var out bytes.Buffer
	err := Evaluate(context.Background(), &out, ".[", []byte(`[]`))
	if err == nil || !strings.Contains(err.Error(), "--jq") {
		t.Fatalf("bad expr: %v", err)
	}
	err = Evaluate(context.Background(), &out, ".", []byte("NAME  API\ndev   x\n"))
	if err == nil || !strings.Contains(err.Error(), "needs JSON output") {
		t.Fatalf("table input: %v", err)
	}
}

type failWriter struct{}

var errWrite = errors.New("write failed")

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestFilterReturnsWriteErrors(t *testing.T) {
	doc := []byte(`[{"name":"dev"}]`)
	for _, expr := range []string{".[].name", "."} {
		if err := Evaluate(context.Background(), failWriter{}, expr, doc); !errors.Is(err, errWrite) {
			t.Errorf("%s: err = %v, want %v", expr, err, errWrite)
		}
	}
}
