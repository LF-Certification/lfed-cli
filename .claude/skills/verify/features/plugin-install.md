# Install a plugin

`lfed plugin install <source>` puts a plugin into the plugin dir so `lfed <name>` runs it. The source is a local executable path, a catalog name (`<name>`, `<name>@<version>`, `<catalog>/<name>`), or a full OCI reference. Success is silent unless stdout is a TTY.

## Sub-features

- `install-path` installs a local executable, unsigned and offline.
- `install-reinstall` installs the same path again and replaces the plugin.
- `install-catalog` installs `monkeysay` from the default published catalog, signature verified.
- `install-file-catalog` installs `monkeysay` through a file catalog built by `make catalog`.
- `install-oci` installs `monkeysay` by its full OCI reference with a named signer.
- `install-bad-source` rejects an argument that is not a path, a name, or an OCI ref.
- `install-multi` installs several sources in one command.

## How to get to it (user POV)

- `lfed plugin install ./lfed-hello` (path must start with `./`, `../`, `/`, or `~/`).
- `lfed plugin install monkeysay` or `lfed plugin install monkeysay@<version>`.
- `lfed plugin install monkeysay --catalog ./catalog.json`.
- `lfed plugin install ghcr.io/lf-certification/lfed-cli-plugins/monkeysay --certificate-identity-regexp <re> --certificate-oidc-issuer <url>`.

## Driving it with lfed-verify

Preconditions:

- Baseline from the README. Empty isolated plugin dir.
- Networked sub-features need https to ghcr.io and the Sigstore TUF root.

- **install-path.** Run `lfed-verify run <id> install-path -- plugin install ./src/lfed-hello`. Exit `0`, empty stdout and stderr. Then `lfed-verify run <id> install-path-list -- plugin list -o json`: one object with `"name": "hello"`, `"version": "0.0.1"`, `"source": {"type": "path", "path": "<scratch>/src/lfed-hello"}`, `"compatible": true`. Then `lfed-verify snapshot <id> install-path-snapshot`: `hello`, `hello/lfed-hello`, `hello/lfed-plugin.json`. Then `lfed-verify run <id> install-path-exec -- hello a b`: exit `0`, stdout ends with `args: a b`.
- **TTY line.** Run `lfed-verify tty <id> install-path-tty -- plugin install ./src/lfed-hello`. Exit `0`, stdout contains `Installed plugin hello 0.0.1 from path:<scratch>/src/lfed-hello`.
- **install-reinstall.** After install-path, run the same install again. Exit `0`; `plugin list -o json` still has exactly one `hello`; the snapshot has no leftover `.prev-hello-*` dir.
- **install-catalog.** Run `lfed-verify run <id> install-catalog -- plugin install monkeysay`. Exit `0`. `plugin list -o json` has `"name": "monkeysay"` with `"source": {"type": "catalog", "ref": "ghcr.io/lf-certification/lfed-cli-plugins/monkeysay", ...}`. Then `lfed-verify run <id> install-catalog-exec -- monkeysay hi`: exit `0`, stdout shows the speech balloon containing `hi`.
- **install-file-catalog.** From the repo root run `make catalog` (writes `catalog/catalog.json`). Run `lfed-verify run <id> install-file-catalog -- plugin install monkeysay --catalog <repo>/catalog/catalog.json`. Exit `0`, same list proof as install-catalog. This is the path `make catalog-verify` exercises.
- **install-oci.** Run `lfed-verify run <id> install-oci -- plugin install ghcr.io/lf-certification/lfed-cli-plugins/monkeysay --certificate-identity-regexp '^https://github.com/LF-Certification/lfed-cli-plugin-monkeysay/' --certificate-oidc-issuer https://token.actions.githubusercontent.com`. Exit `0`; list shows `"type": "oci"`.
- **install-bad-source.** Run `lfed-verify run <id> install-bad-source -- plugin install Not_A_Source`. Exit `1`, stderr starts `lfed: unknown plugin source "Not_A_Source"`. Snapshot shows an empty plugin dir.
- **install-multi.** Mix one local and one catalog source. Run `lfed-verify tty <id> install-multi-ok -- plugin install ./src/lfed-hello monkeysay`. Exit `0`, one `Installed plugin` line per source, and `plugin list -o json` has both `hello` and `monkeysay`.
- **install-multi partial failure.** From an empty plugin dir run `lfed-verify run <id> install-multi-partial -- plugin install ./src/lfed-hello ./src/lfed-missing Not_A_Source monkeysay`. Exit `1`. Stderr has one line per failed source, each starting with that source (`./src/lfed-missing: stat ...`, `Not_A_Source: unknown plugin source ...`); the first carries the `lfed: ` prefix. `plugin list -o json` and the snapshot still show `hello` and `monkeysay`: a failure does not stop or undo the others.
- **install-multi single source.** Run `lfed-verify run <id> install-single-bad -- plugin install ./src/lfed-missing`. Exit `1`, stderr `lfed: stat <scratch>/src/lfed-missing: no such file or directory` with no source prefix. One source keeps the pre-multi error text.

## Gotchas

- On a build that predates multi-source install, the `install-multi` journeys fail with exit `2` and stderr `lfed: accepts 1 arg(s), received 2`. Check `doctor` and the recorded `revision` before reading that as a regression.
- A catalog name that the catalog does not list is exit `1` in a batch (`lfed: <name>: plugin "<name>" is not in the plugin catalog ...`), not `2`.
- The offline pair `./src/lfed-hello ./src/lfed-hello` installs the same name twice. It has not been driven; record what it does before relying on it.
- Without a TTY, a successful install prints nothing. Silence plus exit `0` is success; always add the list and snapshot views.
- A relative path resolves against the scratch dir, because the helper runs lfed there. Use `./src/lfed-hello`, or an absolute path.
- A bare word such as `hello` is a catalog name, not a file. Without `./` lfed goes to the network to resolve it.
- Local installs run `<bin> __metadata` to read the manifest. A path that is not an lfed plugin fails there, not at parse time.
- `install-catalog` resolves the default catalog from `LFED_CATALOG`, then `config.yaml`, then the built-in default. The helper unsets `LFED_CATALOG` and uses an empty config dir, so the built-in default applies.
- Networked journeys fail without network or when GitHub's registry rate-limits anonymous pulls. Report them unproven with stderr; do not add `--allow-unsigned`.
- `install-oci` signer flags must match the publishing workflow identity; a wrong regexp is a verification failure by design.
