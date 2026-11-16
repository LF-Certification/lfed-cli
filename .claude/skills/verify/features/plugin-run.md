# Run an installed plugin

An installed plugin becomes a top-level command: `lfed <name> [args]` runs the plugin binary with lfed's environment. This is the strongest proof an install worked, since it is what the user installed it for.

## Sub-features

- `run-args` passes arguments through to the plugin.
- `run-env` hands the plugin `LFED_*` variables, including the isolated plugin and config dirs.
- `run-exit` propagates the plugin's exit code.
- `run-unknown` errors for a name that is not installed.

## How to get to it (user POV)

- `lfed hello a b` after installing the hello plugin.
- `lfed monkeysay hi` after installing monkeysay.

## Driving it with lfed-verify

Preconditions:

- install-path has run on this run id, so `hello` is installed.

- **run-args.** `lfed-verify run <id> run-args -- hello a b`. Exit `0`, last stdout line `args: a b`.
- **run-env.** Same output. Stdout includes `LFED_PLUGIN_DIR=<scratch>/plugins` and `LFED_CONFIG_DIR=<scratch>/config`, proving the plugin saw the isolated dirs.
- **run-exit.** `lfed-verify run <id> run-exit -- hello fail`. Exit `3`.
- **run-unknown.** `lfed-verify run <id> run-unknown -- nosuchplugin`. Exit `2`, stderr `lfed: unknown command "nosuchplugin" for "lfed"`.

## Gotchas

- `lfed hello wait` prints `ready` and sleeps for an hour. Do not drive it through `run`, which waits for exit. If you need it, start it yourself, record its PID, and kill that PID.
- `lfed hello -o json` returns `{"args": [...], "env": [...]}` for stable assertions.
- After `plugin remove hello`, `lfed hello` is `run-unknown`. That is the remove proof.
