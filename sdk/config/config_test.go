package config

import (
	"strings"
	"testing"
)

func TestContext(t *testing.T) {
	t.Setenv("LFED_CONTEXT", "staging")
	got, err := Context()
	if err != nil || got != "staging" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestContextMissing(t *testing.T) {
	t.Setenv("LFED_CONTEXT", "")
	_, err := Context()
	if err == nil || !strings.Contains(err.Error(), "LFED_CONTEXT") || !strings.Contains(err.Error(), `lfed context use <name>`) {
		t.Fatalf("got %v", err)
	}
}

func TestAPIBaseURLTrimsSlash(t *testing.T) {
	t.Setenv("LFED_API_BASE_URL", "https://api.example.test/")
	got, err := APIBaseURL()
	if err != nil || got != "https://api.example.test" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAPIBaseURLMissing(t *testing.T) {
	t.Setenv("LFED_API_BASE_URL", "")
	_, err := APIBaseURL()
	if err == nil || !strings.Contains(err.Error(), "LFED_API_BASE_URL") {
		t.Fatalf("got %v", err)
	}
}

func TestDebug(t *testing.T) {
	t.Setenv("LFED_DEBUG", "")
	if Debug() {
		t.Fatal("debug on with empty var")
	}
	t.Setenv("LFED_DEBUG", "1")
	if !Debug() {
		t.Fatal("debug off with var set")
	}
}
