# Remove a plugin

`lfed plugin remove <name>` deletes an installed plugin's dir so the name is no longer a command. Success is silent unless stdout is a TTY.

## Sub-features

- `remove-installed` removes a plugin that is installed.
- `remove-missing` errors for a name that is not installed.

## How to get to it (user POV)

- `lfed plugin remove hello`

## Driving it with lfed-verify

Preconditions:

- install-path has run on this run id.

- **remove-installed.** `lfed-verify run <id> remove -- plugin remove hello`. Exit `0`, empty stdout. Then `lfed-verify run <id> remove-list -- plugin list -o json` gives `[]`, `lfed-verify snapshot <id> remove-snapshot` shows an empty plugin dir, and `lfed-verify run <id> remove-exec -- hello` exits `2` with `unknown command "hello"`.
- **TTY line.** Reinstall, then `lfed-verify tty <id> remove-tty -- plugin remove hello`. Stdout contains `Removed plugin hello`.
- **remove-missing.** `lfed-verify run <id> remove-missing -- plugin remove hello` when nothing is installed. Exit `2`, stderr `lfed: unknown command "hello" for "lfed"; run "lfed plugin list"`.

## Gotchas

- Remove takes the plugin name, not the path or the source it was installed from.
- Removing a plugin is the only way to install the same name from a different catalog.
