package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/authflow"
	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/credstore"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in and manage credentials",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newLoginCmd(f), newLogoutCmd(f), newStatusCmd(f), newTokenCmd(f))
	return cmd
}

func newLoginCmd(f *cmdutil.Factory) *cobra.Command {
	var withToken, noBrowser, insecure bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in with the Auth0 device code flow",
		Long: cmdutil.Long(`
			Log in to the active context with the Auth0 device code flow.

			On a terminal, lfed opens the verification URL in the default browser
			after you press Enter. Without a terminal, lfed prints the URL and
			the code.

			lfed stores the credentials in the system keychain.
		`),
		Example: cmdutil.Examples(`
			# Log in with the browser
			$ lfed auth login

			# Print the verification URL and do not open a browser
			$ lfed auth login --no-browser

			# Log in on a headless machine with a refresh token
			$ echo "$REFRESH_TOKEN" | lfed auth login --with-token
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, store, err := cmdutil.OpenStore(f)
			if err != nil {
				return err
			}
			errOut := f.IOStreams.ErrOut
			if withToken {
				line, err := bufio.NewReader(f.IOStreams.In).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("read refresh token from stdin: %w", err)
				}
				if err := authflow.TokenLogin(cmd.Context(), c, store, line, insecure); err != nil {
					return err
				}
				fmt.Fprintf(errOut, "Logged in to %s with a supplied refresh token.\n", c.Name)
				return nil
			}
			identity, err := authflow.DeviceLogin(cmd.Context(), c, store, authflow.LoginOptions{
				IO:          f.IOStreams,
				OpenBrowser: !noBrowser,
				Insecure:    insecure,
			})
			if err != nil {
				return err
			}
			if identity != "" {
				fmt.Fprintf(errOut, "Logged in to %s as %s.\n", c.Name, identity)
			} else {
				fmt.Fprintf(errOut, "Logged in to %s.\n", c.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read a refresh token from stdin. Skip the browser flow.")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the verification URL. Do not open a browser.")
	cmd.Flags().BoolVar(&insecure, "insecure-storage", false, "Store the credentials in a plain file under the lfed state directory. Skip the system keychain.")
	return cmd
}

func newLogoutCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credentials for the active context",
		Long: cmdutil.Long(`
			Remove the stored credentials for the active context.

			lfed removes the credentials from the system keychain and from the
			--insecure-storage file.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, store, err := cmdutil.OpenStore(f)
			if err != nil {
				return err
			}
			if err := authflow.Logout(cmd.Context(), store); err != nil {
				return err
			}
			fmt.Fprintf(f.IOStreams.ErrOut, "Logged out of %s.\n", c.Name)
			return nil
		},
	}
}

type statusRow struct {
	Context  string            `json:"context"`
	Username string            `json:"username"`
	Issuer   string            `json:"issuer"`
	Storage  credstore.Backend `json:"storage"`
	Expires  time.Time         `json:"expires"`
}

func newStatusCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the login status for the active context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			c, store, err := cmdutil.OpenStore(f)
			if err != nil {
				return err
			}
			token, err := authflow.Token(cmd.Context(), c, store)
			if err != nil {
				return err
			}
			_, backend, err := store.LoadCredentials()
			if err != nil {
				return err
			}
			claims, err := authflow.DecodeClaims(token)
			if err != nil {
				return err
			}
			row := statusRow{
				Context:  string(c.Name),
				Username: claims.Username,
				Issuer:   claims.Issuer,
				Storage:  backend,
				Expires:  claims.Expiry,
			}
			return output.PrintOne(p, row, []output.Column[statusRow]{
				{Header: "Context", Value: func(r statusRow) string { return r.Context }},
				{Header: "Username", Value: func(r statusRow) string { return r.Username }},
				{Header: "Issuer", Value: func(r statusRow) string { return r.Issuer }},
				{Header: "Storage", Value: func(r statusRow) string { return string(r.Storage) }},
				{Header: "Access token expires", Value: func(r statusRow) string { return r.Expires.Local().Format(time.RFC3339) }},
			})
		},
	}
}

func newTokenCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print a valid access token for the active context",
		Long: cmdutil.Long(`
			Print a valid access token for the active context.

			lfed refreshes the token when it expires within 60 seconds. A plugin
			runs "$LFED_REEXEC auth token" to get a token.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := f.Token(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(f.IOStreams.Out, token)
			return err
		},
	}
}
