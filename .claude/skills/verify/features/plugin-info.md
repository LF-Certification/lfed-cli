# Show plugin provenance

`lfed plugin info <name>` shows what lfed recorded when it installed a plugin: its source, catalog owner and homepage, OCI digest, verified signer, and the repository and commit of its source code (CODE, with BASIS saying who vouches for it). It reads only the install record in the plugin dir and never contacts a registry. With `-o json` the keys `catalog`, `owner` and `homepage` are the ones `plugin search` uses; they are present only for a catalog install.

## Sub-features

- `info-path` shows a path install: CATALOG `-`, CODE `unknown: a local file carries no source facts`, verification `local`, no catalog keys in JSON. Offline.
- `info-catalog` shows a signed catalog install of `monkeysay`: CODE is the lfed-cli-plugin-monkeysay repo at the certificate's commit, BASIS `signing certificate`, verification `signed`. Networked.
- `info-keys` shows that `catalog`, `owner` and `homepage` from `info` equal those from `plugin search` for the same plugin. Networked.
- `info-unrecorded` shows a record with no provenance: CODE `not recorded: run "lfed plugin install <arg>" again`, with no version or digest pin in `<arg>`. Needs a catalog install first.
- `info-missing` shows the error for a plugin that is not installed.

## How to get to it (user POV)

- `lfed plugin info <name>`
- `lfed plugin info <name> -o json`
- `lfed plugin info <name> --jq .source_url`

## Driving it with lfed-verify

Preconditions:

- Baseline from the README. `S` below is the scratch dir that launch prints.

- **info-path.** `lfed-verify run <id> info-path-install -- plugin install ./src/lfed-hello`, then `lfed-verify run <id> info-path -- plugin info hello` and `lfed-verify run <id> info-path-json -- plugin info hello -o json`. Exit `0` each. Table rows `CATALOG        -` and `CODE           unknown: a local file carries no source facts`, an `INSTALLED` row, and no `OWNER`, `DIGEST`, `BASIS` or `SIGNER` row. JSON has `"verification": "local"`, `"provenance": {"installed_at": ...}`, and no `catalog`, `owner`, `homepage` or `source_url` key.
- **info-catalog.** `lfed-verify run <id> info-catalog-install -- plugin install monkeysay`, then `lfed-verify run <id> info-catalog -- plugin info monkeysay` and `lfed-verify run <id> info-catalog-json -- plugin info monkeysay --jq '{verification, source_url}'`. Exit `0` each. Table rows `CATALOG        default`, `OWNER          LF-Certification/platform-engineering`, `CODE           https://github.com/LF-Certification/lfed-cli-plugin-monkeysay/tree/<sha>` where `<sha>` equals the `REVISION` row, `BASIS          signing certificate`, and a `SIGNER` row under `https://github.com/LF-Certification/lfed-cli-plugin-monkeysay/.github/workflows/release.yml@refs/tags/v`. JSON `"verification":"signed"` and `source_url` equal to CODE.
- **info-keys.** After info-catalog, `lfed-verify run <id> info-keys-info -- plugin info monkeysay --jq '{catalog, owner, homepage}'` and `lfed-verify run <id> info-keys-search -- plugin search monkeysay --jq '.[] | select(.name=="monkeysay") | {catalog, owner, homepage}'`. Exit `0` each and the two stdout lines are identical: `{"catalog":"default","homepage":"https://github.com/LF-Certification/lfed-cli-plugin-monkeysay","owner":"LF-Certification/platform-engineering"}`.
- **info-unrecorded.** After info-catalog, drop the provenance from the record the way an older lfed wrote it: `python3 -c 'import json,sys; p=sys.argv[1]; d=json.load(open(p)); d.pop("provenance",None); json.dump(d,open(p,"w"),indent=2)' "$S/plugins/monkeysay/lfed-plugin.json"`. Then `lfed-verify run <id> info-unrecorded -- plugin info monkeysay` and `lfed-verify run <id> info-unrecorded-json -- plugin info monkeysay --jq '{verification, provenance, catalog, owner}'`. Exit `0` each. Table row `CODE           not recorded: run "lfed plugin install monkeysay" again`, with no `INSTALLED`, `OWNER` or `BASIS` row. JSON `{"catalog":"default","owner":null,"provenance":null,"verification":"not-recorded"}`; the owner was never recorded, so the key is absent. To see the hint for a non-default catalog, also set the alias: `python3 -c 'import json,sys; p=sys.argv[1]; d=json.load(open(p)); d["source"]["catalog"]="acme"; json.dump(d,open(p,"w"),indent=2)' "$S/plugins/monkeysay/lfed-plugin.json"`, then `lfed-verify run <id> info-unrecorded-alias -- plugin info monkeysay`: rows `CATALOG        acme` and `CODE           not recorded: run "lfed plugin install acme/monkeysay" again`. Snapshot after each edit.
- **info-missing.** `lfed-verify run <id> info-missing -- plugin info nosuch`. Exit `2`, stderr `lfed: unknown command "nosuch" for "lfed"; run "lfed plugin list"`.

## Gotchas

- `info` reads the record as it is on disk. The two record edits stand in for a plugin installed by an older lfed, since no old binary is available; they are the only sanctioned hand edits to the plugin dir. Remove or reinstall monkeysay afterwards, or use a fresh run id.
- `owner` and `homepage` are the catalog entry at install time. `info-keys` matches only while the live catalog entry is unchanged since the install; a mismatch after a catalog change is expected, not a bug.
- `--jq` prints `null` for an absent key, so assert absence on `-o json` output, as info-path does.
- The not-installed message says "unknown command" because `lfed <name>` dispatch and `plugin info` share one not-found error, which also sets exit `2`.
- info-catalog, info-keys and info-unrecorded are networked. Without ghcr.io and the Sigstore TUF root, report them unproven with the install's stderr.
