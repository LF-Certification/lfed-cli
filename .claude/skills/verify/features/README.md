# lfed plugin verification map

This directory is the maintained source for verifying lfed plugin management from the built binary. Read this index before driving, then use the matching feature file as the recipe.

## Baseline preconditions

- Run `.claude/skills/verify/lfed-verify launch <run-id>` and `lfed-verify doctor <run-id>` reports `doctor: ok`.
- The isolated plugin dir is empty unless a recipe's preconditions say otherwise.
- `./src/lfed-hello` (relative to the scratch dir, which is every command's cwd) is the locally built test plugin. Its name is `hello`, version `0.0.1`.
- Offline journeys use only `./src/lfed-hello`. Networked journeys use only the public sample catalog entry `monkeysay` (`catalog/plugins/monkeysay.yaml`) and need https to ghcr.io and the Sigstore TUF root.

## Driving conventions

- Every command below is literal and runs as `lfed-verify run <run-id> <label> -- <args>`. `lfed-verify` means `.claude/skills/verify/lfed-verify`.
- Use `-o json` for every assertion on list or upgrade output. The table output is for humans.
- Use `lfed-verify tty` only to observe TTY-only lines. Use `run` for everything else.
- Start a new run id per journey when a journey needs an empty plugin dir, or remove what the previous journey installed.

## Proof and skip reporting

- Each step's proof is the recorded `cmd`, `stdout`, `stderr`, and `exit`, plus a `snapshot` after any mutation.
- A mutation is proven only with a read-only second view: `plugin list -o json`, a snapshot, or running the plugin.
- Report a networked journey that could not reach the network as unproven, with the stderr. Never report it as verified through the local-path journey.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible behavior, then exactly four H2 sections in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with lfed-verify`, `Gotchas`.

## Features

- [Install a plugin](./plugin-install.md) covers local path, catalog name, file catalog, OCI ref, reinstall, bad sources, and several arguments in one command. Primary feature.
- [List plugins](./plugin-list.md) covers the JSON and table views that prove an install landed.
- [Run an installed plugin](./plugin-run.md) covers dispatch of `lfed <name>` to the plugin binary.
- [Remove a plugin](./plugin-remove.md) covers removal and the not-installed error.
- [Upgrade plugins](./plugin-upgrade.md) covers the dry-run plan and the local-plugin note.
- [Show plugin provenance](./plugin-info.md) covers `plugin info` for path, signed catalog, and unrecorded installs, its JSON keys against `plugin search`, and the not-installed error.
