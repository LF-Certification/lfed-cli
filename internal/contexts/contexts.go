package contexts

import (
	"errors"
	"fmt"
	"strings"
)

type Name string

const (
	Dev     Name = "dev"
	Staging Name = "staging"
	Prod    Name = "prod"
)

type Context struct {
	Name Name

	Auth0Domain string

	Issuer string

	Audience string

	ClientID string
}

func (c Context) APIBaseURL() string {
	return strings.TrimSuffix(c.Audience, "/")
}

var table = []Context{
	{
		Name:        Dev,
		Auth0Domain: "linuxfoundation-dev.auth0.com",
		Issuer:      "https://linuxfoundation-dev.auth0.com/",
		Audience:    "https://api.dev.ed.linuxfoundation.org/",
		ClientID:    "0TN1OElqQY146vLEPdV5qfejRKpc9IAZ",
	},
	{
		Name:        Staging,
		Auth0Domain: "linuxfoundation-staging.auth0.com",
		Issuer:      "https://linuxfoundation-staging.auth0.com/",
		Audience:    "https://api.staging.ed.linuxfoundation.org/",
		ClientID:    "9XzXgDfAB9O7IoHqhBj5mg4VLvdBM8ci",
	},
	{
		Name:        Prod,
		Auth0Domain: "sso.linuxfoundation.org",
		Issuer:      "https://sso.linuxfoundation.org/",
		Audience:    "https://api.ed.linuxfoundation.org/",
		ClientID:    "kkCpM0c9zJ0vNZZDDOGqcyzocOBircOn",
	},
}

var ErrNoContext = errors.New("no context set; run `lfed context use <dev|staging|prod>`")

func All() []Context {
	out := make([]Context, len(table))
	copy(out, table)
	return out
}

func Names() []string {
	out := make([]string, len(table))
	for i, c := range table {
		out[i] = string(c.Name)
	}
	return out
}

func Lookup(name string) (Context, error) {
	for _, c := range table {
		if string(c.Name) == name {
			return c, nil
		}
	}
	return Context{}, fmt.Errorf("unknown context %q; expected one of %s", name, strings.Join(Names(), ", "))
}

func Resolve(flag, env, stored string) (Context, error) {
	for _, candidate := range []string{flag, env, stored} {
		if candidate != "" {
			return Lookup(candidate)
		}
	}
	return Context{}, ErrNoContext
}
