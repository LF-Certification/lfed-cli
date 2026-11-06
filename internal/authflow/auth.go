package authflow

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cli/browser"
	"golang.org/x/oauth2"

	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/internal/credstore"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
)

var Scopes = []string{"openid", "email", "profile", "offline_access"}

var ErrNotLoggedIn = errors.New(`not logged in; run "lfed auth login"`)

var ErrSessionExpired = errors.New(`session expired or revoked; run "lfed auth login"`)

var ErrNoRefreshToken = errors.New(`no refresh token stored; run "lfed auth login"`)

const httpTimeout = 30 * time.Second

func withHTTPClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: httpTimeout})
}

func oauthConfig(c contexts.Context) *oauth2.Config {
	base := strings.TrimSuffix(c.Issuer, "/")
	return &oauth2.Config{
		ClientID: c.ClientID,
		Scopes:   Scopes,
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: base + "/oauth/device/code",
			TokenURL:      base + "/oauth/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

type LoginOptions struct {
	IO *iostreams.IOStreams

	OpenBrowser bool

	Insecure bool
}

func DeviceLogin(ctx context.Context, c contexts.Context, store credstore.Store, opts LoginOptions) (string, error) {
	cfg := oauthConfig(c)
	httpCtx := withHTTPClient(ctx)
	resp, err := cfg.DeviceAuth(httpCtx, oauth2.SetAuthURLParam("audience", c.Audience))
	if err != nil {
		return "", fmt.Errorf("request device code: %w", err)
	}
	if err := printDeviceCodeInstructions(opts.IO, resp, opts.OpenBrowser && opts.IO.IsStdinTTY()); err != nil {
		return "", fmt.Errorf("display login instructions: %w", err)
	}
	fmt.Fprintln(opts.IO.ErrOut, "Waiting for authentication...")

	token, err := cfg.DeviceAccessToken(httpCtx, resp)
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", errors.New("timed out waiting for login")
		case errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "expired_token":
			return "", errors.New("timed out waiting for login")
		case errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "access_denied":
			return "", errors.New("login was denied")
		default:
			return "", fmt.Errorf("wait for login: %w", err)
		}
	}

	if token.RefreshToken == "" {
		return "", errors.New("login did not return a refresh token; offline access may be disabled for this API")
	}

	if err := persistLogin(ctx, store, credstore.Credentials{
		RefreshToken:      token.RefreshToken,
		AccessToken:       token.AccessToken,
		AccessTokenExpiry: token.Expiry,
	}, opts.Insecure); err != nil {
		return "", err
	}

	idToken, _ := token.Extra("id_token").(string)
	return IdentityFromIDToken(idToken), nil
}

func TokenLogin(ctx context.Context, c contexts.Context, store credstore.Store, refreshToken string, insecure bool) error {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return errors.New("no refresh token provided on stdin")
	}
	token, err := oauthConfig(c).TokenSource(withHTTPClient(ctx), &oauth2.Token{RefreshToken: refreshToken}).Token()
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
		return errors.New("refresh token rejected by the issuer")
	}
	if err != nil {
		return fmt.Errorf("validate refresh token: %w", err)
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return persistLogin(ctx, store, credstore.Credentials{
		RefreshToken:      token.RefreshToken,
		AccessToken:       token.AccessToken,
		AccessTokenExpiry: token.Expiry,
	}, insecure)
}

func persistLogin(ctx context.Context, store credstore.Store, creds credstore.Credentials, insecure bool) error {
	unlock, err := store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.SaveCredentials(creds, insecure); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return nil
}

func Logout(ctx context.Context, store credstore.Store) error {
	unlock, err := store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.DeleteCredentials(); err != nil {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}

func Token(ctx context.Context, c contexts.Context, store credstore.Store) (string, error) {
	unlock, err := store.Lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	creds, backend, err := store.LoadCredentials()
	if errors.Is(err, credstore.ErrNotFound) {
		return "", ErrNotLoggedIn
	}
	if err != nil {
		return "", err
	}
	if creds.ValidAccessToken() {
		return creds.AccessToken, nil
	}
	if creds.RefreshToken == "" {
		return "", ErrNoRefreshToken
	}

	refreshed, err := oauthConfig(c).TokenSource(withHTTPClient(ctx), &oauth2.Token{RefreshToken: creds.RefreshToken}).Token()
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.ErrorCode == "invalid_grant" {
		return "", ErrSessionExpired
	}
	if err != nil {
		return "", fmt.Errorf("refresh access token: %w", err)
	}

	refreshToken := refreshed.RefreshToken
	if refreshToken == "" {
		refreshToken = creds.RefreshToken
	}
	if err := store.SaveCredentials(credstore.Credentials{
		RefreshToken:      refreshToken,
		AccessToken:       refreshed.AccessToken,
		AccessTokenExpiry: refreshed.Expiry,
	}, backend == credstore.File); err != nil {
		return "", fmt.Errorf("save refreshed credentials: %w", err)
	}
	return refreshed.AccessToken, nil
}

func printDeviceCodeInstructions(ios *iostreams.IOStreams, resp *oauth2.DeviceAuthResponse, openInBrowser bool) error {
	w := ios.ErrOut
	if _, err := fmt.Fprintf(w, "%s First copy your one-time code: %s\n", ios.ColorScheme().WarningIcon(), terminalSafeText(resp.UserCode)); err != nil {
		return err
	}

	verificationURI := resp.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = resp.VerificationURI
	}
	displayURI := terminalSafeText(verificationURI)
	if !openInBrowser {
		_, err := fmt.Fprintf(w, "Then visit: %s\n", displayURI)
		return err
	}
	if _, err := fmt.Fprintf(w, "Press Enter to open %s in your browser...", displayURI); err != nil {
		return err
	}
	if _, err := bufio.NewReader(ios.In).ReadString('\n'); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("wait for Enter: %w", err)
	}
	if err := openBrowser(verificationURI); err != nil {
		_, writeErr := fmt.Fprintf(w, "Failed opening a web browser at %s\n  %v\n  Please try entering the URL in your browser manually\n", displayURI, err)
		return writeErr
	}
	return nil
}

func openBrowser(uri string) error {
	if err := validateBrowserURL(uri); err != nil {
		return err
	}
	if prog := os.Getenv("BROWSER"); prog != "" {
		return exec.Command(prog, uri).Run() // #nosec G204
	}
	return browser.OpenURL(uri)
}

func terminalSafeText(s string) string {
	if strings.ContainsFunc(s, unicode.IsControl) {
		return strconv.QuoteToASCII(s)
	}
	return s
}

func validateBrowserURL(uri string) error {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || strings.ContainsFunc(uri, unicode.IsControl) {
		return errors.New("refusing to open verification URI: expected an absolute HTTPS URL with a hostname")
	}
	return nil
}
