---
name: verify
description: Drive the real lfed CLI binary to prove plugin management works the way a user sees it, starting with `lfed plugin install` (local path, catalog name, OCI ref) and the commands that prove an install took effect (plugin list, running the plugin, plugin remove, plugin upgrade), and `lfed plugin info`, which shows where an installed plugin and its source code came from. Use after changing anything under internal/cmd/plugin or internal/pluginmgr, or whenever a claim about plugin behavior needs evidence from the built binary rather than unit tests.
---

# Verify lfed plugin management

The surface is a short-lived Go CLI, `bin/lfed`. There is no server. Every command runs in an isolated set of directories so a run never reads or writes the user's real plugin dir, config, credentials, or caches. `make test` covers units; this skill proves the built binary.

All steps go through the helper `.claude/skills/verify/lfed-verify` (run from the repo root). Read [features/README.md](features/README.md) before driving, then use the matching feature file as the recipe.

## Launch

```sh
.claude/skills/verify/lfed-verify launch [<run-id>]
```

It runs `make build` (writes `bin/lfed`), builds the test plugin `internal/pluginmgr/testdata/helloplugin` to `<scratch>/src/lfed-hello`, and creates the scratch dir `${TMPDIR:-/tmp}/lfed-verify-<run-id>`. Ready is the final line `ready`; the lines above it print the run id, scratch dir, and evidence dir. Omit the run id to get a timestamped one. Launch refuses a run id whose scratch dir exists, so two runs never share state; parallel runs are safe with distinct ids.

Isolation, applied by `run`, `tty`, `snapshot`, and `doctor`:

- `LFED_PLUGIN_DIR=<scratch>/plugins`, `LFED_CONFIG_DIR=<scratch>/config`, `LFED_STATE_DIR=<scratch>/state`.
- `XDG_CACHE_HOME=<scratch>/cache`. lfed keeps the verified catalog cache and the Sigstore TUF root under `os.UserCacheDir()`, which reads this variable on Linux. On macOS `os.UserCacheDir()` ignores it and uses `~/Library/Caches`, so a networked journey there writes the real cache.
- `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME` also point into scratch. `LFED_CATALOG` and `LFED_CONTEXT` are unset.
- The working directory is the scratch dir, so `./src/lfed-hello` names the built test plugin.

## Doctor

```sh
.claude/skills/verify/lfed-verify doctor <run-id>
```

Read-only. Checks `bin/lfed` runs, no `.go` file under `cmd`, `internal`, or `sdk` is newer than `bin/lfed` (a stale binary proves nothing about your change; rerun `make build`), the scratch dir and test plugin exist, and the evidence dir exists. It prints how many plugins the isolated plugin dir holds. Exit 0 and `doctor: ok` means drive.

## Drive

```sh
.claude/skills/verify/lfed-verify run <run-id> <label> -- <lfed args...>
.claude/skills/verify/lfed-verify tty <run-id> <label> -- <lfed args...>
.claude/skills/verify/lfed-verify snapshot <run-id> <label>
```

- `run` executes `bin/lfed <args>` with stdin from `/dev/null` and stdout to a file, so stdout is not a TTY. It prints and records the command, stdout, stderr, and exit code.
- `tty` runs the same command under a PTY with `script -qefc` from util-linux. Use it to observe lines lfed prints only to a terminal: `plugin install` and `plugin remove` print their `✓ Installed plugin ...` and `✓ Removed plugin ...` lines only when stdout is a TTY. Under `tty`, stdout and stderr arrive merged in `stdout`, with ANSI colors and CRLF line ends.
- `snapshot` records the isolated plugin dir tree and every `<name>/lfed-plugin.json`.

Without a TTY, `plugin install` succeeds silently. Prove success with all three of the exit code, `plugin list -o json`, and a `snapshot` showing `<name>/lfed-<name>` and `<name>/lfed-plugin.json`.

Labels become evidence subdirectories. Use `<feature>-<step>`, for example `install-path-list`.

## Evidence

Evidence lives at `.verify-evidence/<run-id>/` in the repo root, which `.gitignore` excludes. Each `run` or `tty` writes `<label>/cmd`, `stdout`, `stderr`, `exit`. Each `snapshot` writes `<label>/plugin-dir.txt`. Launch writes `version` and `revision` (the git HEAD).

Proof standards:

- Drive the CLI as a user types it. No calls into Go packages, no hand-written plugin dirs.
- Capture the action and the resulting state. An install proof is the install's exit code plus a second, read-only view: `plugin list -o json` and a snapshot, and ideally running the plugin.
- A failing journey is a result. Record it with its stderr and exit code; never loosen the expected outcome to make it pass.
- Networked journeys hit ghcr.io and the Sigstore TUF root for real. Do not swap in `--allow-unsigned` to get past a verify failure; that skips the check under test.

## Cleanup

```sh
.claude/skills/verify/lfed-verify cleanup <run-id>
```

Removes only `${TMPDIR:-/tmp}/lfed-verify-<run-id>`. The evidence dir stays. Nothing long-running is started, so there are no processes to kill, except `lfed hello wait`, which sleeps for an hour; if you drive it, kill the PID you started, never by name. Run cleanup after a failed attempt too. Delete `.verify-evidence/<run-id>` by hand once the proof is no longer needed.

## Helpers

- `.claude/skills/verify/lfed-verify` is the only helper. Run it with no arguments for usage. It needs bash, go, make, python3 (doctor's count), and `script` from util-linux (only for `tty`).
