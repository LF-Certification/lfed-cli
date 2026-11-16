# Upgrade plugins

`lfed plugin upgrade [<name>...]` resolves each installed plugin against the source recorded at install time and installs a newer compatible version. Local and pinned plugins are listed and left alone. It proves an install recorded its source correctly.

## Sub-features

- `upgrade-local-note` reports a path-installed plugin as not upgradable, with the reinstall command.
- `upgrade-dry-run` prints the plan without installing.
- `upgrade-catalog` checks a catalog-installed plugin against the catalog.
- `upgrade-pinned` leaves a `name@version` install alone.

## How to get to it (user POV)

- `lfed plugin upgrade`
- `lfed plugin upgrade --dry-run`
- `lfed plugin upgrade hello`

## Driving it with lfed-verify

Preconditions:

- install-path has run on this run id. Networked sub-features also need install-catalog.

- **upgrade-local-note.** `lfed-verify run <id> upgrade-local -- plugin upgrade -o json`. Exit `0`, one object `"name": "hello"`, `"from": "0.0.1"`, `"upgraded": false`, `"note"` starting `installed from <scratch>/src/lfed-hello; reinstall with "lfed plugin install <scratch>/src/lfed-hello"`.
- **upgrade-dry-run.** `lfed-verify run <id> upgrade-dry -- plugin upgrade --dry-run -o json`. Exit `0`. A snapshot before and after shows the same `lfed-plugin.json`.
- **upgrade-catalog.** After install-catalog, `lfed-verify run <id> upgrade-catalog -- plugin upgrade monkeysay -o json`. Exit `0`; `upgraded` is `false` with a note when already newest.
- **upgrade-pinned.** Install `monkeysay@<version>` (pick an older published version), then `plugin upgrade -o json`. Exit `0`, monkeysay not upgraded, note names the pin.

## Gotchas

- One plugin's failure does not stop the others; the command exits `1` after printing every result. Read every row.
- `--dry-run` on a catalog plugin still contacts the registry to compute the plan. It skips the install, not the network.
