package root

import (
	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
)

func helpTopics() []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "environment",
			Short: "Environment variables that lfed reads and sets",
			Long: cmdutil.Long(`
				lfed reads these variables.

				LFED_CONTEXT
				    The deployment context: dev, staging or prod. The --context flag
				    overrides it. It overrides the config file.

				LFED_CONFIG_DIR
				    The config directory. Default: $XDG_CONFIG_HOME/lfed or
				    ~/.config/lfed.

				LFED_STATE_DIR
				    The directory for the --insecure-storage credential file.
				    Default: $XDG_STATE_HOME/lfed or ~/.local/state/lfed.

				LFED_PLUGIN_DIR
				    The plugin directory. Default: $XDG_DATA_HOME/lfed/plugins or
				    ~/.local/share/lfed/plugins.

				LFED_CATALOG
				    The default plugin catalog: an OCI reference, a file path, or
				    a file:// URL. Default: ` + pluginmgr.DefaultCatalog + `.
				    config.yaml "catalogs.default.ref" sets the same value.

				LFED_DEBUG
				    Set to any value to print debug output on stderr.

				GITHUB_TOKEN, GH_TOKEN
				    A token for ghcr.io. lfed also reads the Docker config and
				    "gh auth token". The token MUST have the read:packages scope.

				NO_COLOR, CLICOLOR=0, TERM=dumb
				    Disable colour. CLICOLOR_FORCE enables colour without a terminal.

				lfed sets these variables for a plugin.

				LFED_CLI_VERSION
				    The lfed version.

				LFED_CONTEXT
				    The active context name, or empty.

				LFED_API_BASE_URL
				    The API base URL of the active context, or empty.

				LFED_OUTPUT
				    The value of --output. Default: table.

				LFED_JQ
				    The value of --jq, or empty.

				LFED_REEXEC
				    The path of the lfed executable. A plugin runs
				    "$LFED_REEXEC auth token" to get an access token.

				LFED_DEBUG
				    "1" when debug output is on, or empty.
			`),
		},
		{
			Use:   "exit-codes",
			Short: "Exit codes of lfed and plugin commands",
			Long: cmdutil.Long(`
				lfed exits with one of these codes.

				0    Success.
				1    An error. The message is on stderr.
				2    A usage error: an unknown command, a bad flag, a missing
				     argument, or a plugin that does not support this lfed.
				130  SIGINT or SIGTERM stopped the command.

				A plugin command exits with the exit code of the plugin.
			`),
		},
		{
			Use:   "plugins",
			Short: "How lfed finds, verifies and runs plugins",
			Long: cmdutil.Long(`
				A plugin is an executable lfed-<name> under the plugin directory.
				"lfed --help" lists the installed plugins under "Plugins".
				"lfed <name> [args]" runs the plugin with the arguments unchanged.
				Run "lfed help environment" for the variables a plugin receives.

				Sources

				"lfed plugin install <name>" looks <name> up in the default
				plugin catalog and pulls the OCI repository that the catalog
				names. "lfed plugin install <catalog>/<name>" looks <name> up in
				the catalog with that alias in config.yaml. A full OCI reference
				installs a plugin that is not in a catalog. A local path installs
				a plugin that you built.

				Plugin catalogs

				A plugin catalog is a signed OCI artifact. The default catalog is
				authored in the catalog/plugins directory of
				github.com/LF-Certification/lfed-cli and published by that
				repository's catalog-publish.yml workflow. lfed verifies the
				catalog against that workflow identity before it reads an entry.

				"lfed plugin catalog add <alias> <ref>" registers another plugin
				catalog, for example one that an organisation publishes for its
				private plugins. Each catalog has its own signer. A name in one
				catalog never shadows a name in another: <alias>/<name> and
				<name> are separate lookups. A plugin records the catalog it
				came from; "lfed plugin upgrade" resolves it there again. To
				install the same name from another catalog, remove the plugin
				first.

				The alias "default" replaces the built-in catalog and its
				signer for a catalog that you publish in its place.

				Signatures

				A plugin from a registry MUST carry a Sigstore signature. The
				signature is an OCI referrer of the plugin index. For a catalog
				plugin, the signer is the identity in its catalog entry with
				{version} replaced by the release version. For a full OCI
				reference, pass --certificate-identity-regexp and
				--certificate-oidc-issuer, or --allow-unsigned. --allow-unsigned
				also skips the plugin catalog signature. lfed does not verify a plugin
				from a local path.

				Files

				Each plugin has the directory <plugin dir>/<name>. It holds the
				binary lfed-<name> and the record lfed-plugin.json with the manifest
				and the source. "lfed plugin upgrade" resolves the source again.
				"lfed plugin remove" deletes the directory.
			`),
		},
	}
}
