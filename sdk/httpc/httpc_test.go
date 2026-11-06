package httpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustNew(t *testing.T, baseURL string, opts ...Option) *Client {
	t.Helper()
	c, err := New(baseURL, "tok", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		sentinel error
		want     string
	}{
		{401, `{"detail":"missing username claim"}`, ErrUnauthorized, "HTTP 401: missing username claim"},
		{403, `{"detail":"nope"}`, ErrForbidden, "HTTP 403: nope"},
		{404, `{"detail":"hosting target not found"}`, ErrNotFound, "HTTP 404: hosting target not found"},
		{409, `{"detail":"already decommissioned"}`, ErrConflict, "HTTP 409: already decommissioned"},
		{401, ``, ErrUnauthorized, "HTTP 401: Unauthorized"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		err := mustNew(t, srv.URL+"/").Do(context.Background(), http.MethodGet, "/x", nil, nil)
		srv.Close()
		if !errors.Is(err, tc.sentinel) {
			t.Errorf("%d: errors.Is failed: %v", tc.status, err)
		}
		if err.Error() != tc.want {
			t.Errorf("%d: got %q want %q", tc.status, err.Error(), tc.want)
		}
	}
}

func TestHTTPErrorIsAndAs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"detail":"busy"}`))
	}))
	defer srv.Close()
	err := mustNew(t, srv.URL).Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is(ErrConflict) false: %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(ErrNotFound) true: %v", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 409 || httpErr.Message != "busy" {
		t.Fatalf("errors.As: got %#v", err)
	}
}

func TestHTTPErrorUnmappedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"detail":"boom"}`))
	}))
	defer srv.Close()
	err := mustNew(t, srv.URL).Do(context.Background(), http.MethodGet, "/x", nil, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 500 || httpErr.Message != "boom" {
		t.Fatalf("got %#v", err)
	}
	if err.Error() != "HTTP 500: boom" {
		t.Errorf("message %q", err.Error())
	}
}

func TestHTTPErrorRawBodyAndValidationList(t *testing.T) {
	bodies := map[string]string{
		`plain text`: "plain text",
		`{"detail":[{"loc":["body"],"msg":"x"}]}`: `[{"loc":["body"],"msg":"x"}]`,
	}
	for body, want := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(body))
		}))
		err := mustNew(t, srv.URL).Do(context.Background(), http.MethodGet, "/x", nil, nil)
		srv.Close()
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Message != want {
			t.Errorf("body %q: got %#v", body, err)
		}
	}
}

func TestDoRoundTripsJSON(t *testing.T) {
	type in struct {
		Status string `json:"status"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/things/a" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "lfed-plugin/placement" {
			t.Errorf("headers %v", r.Header)
		}
		var got in
		if err := decode(r, &got); err != nil || got.Status != "Active" {
			t.Errorf("body %+v %v", got, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"Cordoned"}`))
	}))
	defer srv.Close()
	var out in
	c := mustNew(t, srv.URL, WithUserAgent("placement"), WithHTTPClient(srv.Client()))
	if err := c.Do(context.Background(), http.MethodPatch, "/v1/things/a", in{Status: "Active"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "Cordoned" {
		t.Errorf("got %+v", out)
	}
}

func TestDoNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	var out map[string]any
	if err := mustNew(t, srv.URL).Do(context.Background(), http.MethodDelete, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
}

func TestNewBaseURLValidation(t *testing.T) {
	for _, ok := range []string{"https://api.example.test", "http://localhost:8080/", "http://127.0.0.1", "http://[::1]:9"} {
		if _, err := New(ok, "tok"); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com", "http://api.example.test/", "ftp://x", "", "not a url"} {
		if _, err := New(bad, "tok"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
