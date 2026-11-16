# List plugins

`lfed plugin list` shows each installed plugin with its version, compatibility status, catalog, source, and summary. It is the read-only view that proves an install or removal took effect.

## Sub-features

- `list-empty` shows no plugins in a fresh plugin dir.
- `list-json` returns one object per plugin with manifest fields, `source`, and `compatible`.
- `list-table` shows the human table.

## How to get to it (user POV)

- `lfed plugin list`
- `lfed plugin list -o json`

## Driving it with lfed-verify

Preconditions:

- Baseline from the README.

- **list-empty.** On a fresh run, `lfed-verify run <id> list-empty -- plugin list -o json`. Exit `0`, stdout `[]`.
- **list-json.** After install-path, `lfed-verify run <id> list-json -- plugin list -o json`. Exit `0`, one object, `"name": "hello"`, `"version": "0.0.1"`, `"summary": "Test plugin for pluginmgr"`, `"source": {"type": "path", ...}`, `"compatible": true`.
- **list-table.** `lfed-verify run <id> list-table -- plugin list`. Exit `0`, header `NAME  VERSION  STATUS  CATALOG  SOURCE  SUMMARY`, row `hello  0.0.1  ok  -  path:<scratch>/src/lfed-hello ...`.

## Gotchas

- An empty table still prints the header row. Assert on `-o json`.
- `compatible` depends on the running lfed version. A dev build's version comes from `git describe`; the hello plugin requires `>=0.1.0 <1`, which a `0.1.x` build satisfies.
- The catalog column is `-` for path and OCI installs. Only catalog installs fill it.
