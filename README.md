# lfed-cli

`lfed` is the Linux Foundation Education platform CLI.
The host binary logs in, selects a deployment context and runs plugins.
Plugins are separate binaries that `lfed` installs from signed OCI artifacts.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/LF-Certification/lfed-cli/main/install.sh | bash
```

The script installs the newest release into `~/.local/bin`.
`LFED_VERSION=X.Y.Z` pins a version. `LFED_INSTALL_DIR` changes the directory.
With `cosign` on `PATH`, the script verifies the release signature.
`LFED_REQUIRE_SIGNATURE=1` makes a missing `cosign` an error.

## Usage

```sh
lfed context use dev
lfed auth login
lfed plugin install monkeysay
lfed monkeysay hello
lfed help plugins
```

## Write a plugin

Plugins import the SDK module `go.ed.linuxfoundation.org/lfed/sdk`.
Copy [lfed-cli-plugin-monkeysay](https://github.com/LF-Certification/lfed-cli-plugin-monkeysay) to start.
`lfed help plugins`, `lfed help environment` and `lfed help exit-codes` state the contract.

## Catalog

`catalog/plugins/<name>.yaml` lists each public plugin and its signer identity.
`make catalog` builds and validates `catalog/catalog.json`.
A push to `main` that changes `catalog/` runs `catalog-publish.yml`, which publishes and signs the catalog.
`lfed` pins that workflow identity. You MUST NOT rename the workflow file or the `main` branch.

## Develop

```sh
make build
make lint test tidy-check
make install-local
```

## Release

1. Run `scripts/create-release.sh cli` or `scripts/create-release.sh sdk`. Merge the changelog commit into `main`.
2. Run `scripts/create-release.sh tag cli|sdk` on the merged commit.
3. Push the tag. A `vX.Y.Z` tag runs `release.yml`. An `sdk/vX.Y.Z` tag publishes the SDK through the Go proxy.

## License

Apache-2.0. See [LICENSE](LICENSE).
