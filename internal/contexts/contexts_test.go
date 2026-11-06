package contexts

import (
	"errors"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	cases := []struct {
		name             string
		flag, env, saved string
		want             Name
		wantErr          error
	}{
		{"flag wins", "prod", "staging", "dev", Prod, nil},
		{"env over config", "", "staging", "dev", Staging, nil},
		{"config alone", "", "", "dev", Dev, nil},
		{"nothing set", "", "", "", "", ErrNoContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.flag, tc.env, tc.saved)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tc.want {
				t.Fatalf("name = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func TestResolveRejectsUnknownNameEvenWhenLowerSourceIsValid(t *testing.T) {
	_, err := Resolve("stage", "", "staging")
	if err == nil {
		t.Fatal("expected error for unknown flag value")
	}
}

func TestAPIBaseURLHasNoTrailingSlash(t *testing.T) {
	for _, c := range All() {
		if got := c.APIBaseURL(); got[len(got)-1] == '/' {
			t.Errorf("%s: %q ends with /", c.Name, got)
		}
	}
}
