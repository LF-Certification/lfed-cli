#!/usr/bin/env bash
# Build catalog/catalog.json from catalog/plugins/*.yaml and validate it.
#
# Requires: yq (v4), jq, check-jsonschema.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGINS_DIR="$ROOT/plugins"
SCHEMA_DIR="$ROOT/schema"
OUT="${1:-$ROOT/catalog.json}"

fail() {
  echo "error: $*" >&2
  exit 1
}

require_tools() {
  local tool
  for tool in yq jq check-jsonschema; do
    command -v "$tool" >/dev/null 2>&1 || fail "missing tool: $tool"
  done
}

# Check the invariants that the schema cannot express.
check_entry() {
  local file="$1"
  local stem name ref
  stem="$(basename "$file" .yaml)"
  name="$(yq -r '.name // ""' "$file")"
  ref="$(yq -r '.ref // ""' "$file")"

  [[ "$name" == "$stem" ]] || fail "$file: name '$name' does not equal file stem '$stem'"
  [[ "$ref" == */"$name" ]] || fail "$file: ref '$ref' does not end in '/$name'"
}

validate_entries() {
  local files=("$@")
  check-jsonschema --schemafile "$SCHEMA_DIR/entry.schema.json" "${files[@]}"
}

build() {
  local files=("$@")
  local tmp
  tmp="$(mktemp "$OUT.XXXXXX")"
  trap 'rm -f "$tmp"' RETURN

  # yq -o=json emits one document per file; jq collects, sorts, and wraps.
  yq -o=json -I=0 '.' "${files[@]}" \
    | jq --sort-keys --slurp '{schema_version: 1, plugins: (sort_by(.name))}' \
    > "$tmp"

  local dupes
  dupes="$(jq -r '[.plugins[].name] | group_by(.) | map(select(length > 1) | .[0]) | .[]' "$tmp")"
  [[ -z "$dupes" ]] || fail "duplicate plugin names: $dupes"

  mv "$tmp" "$OUT"
  trap - RETURN
}

validate_catalog() {
  check-jsonschema --schemafile "$SCHEMA_DIR/catalog.schema.json" "$OUT"
}

main() {
  require_tools

  local files=()
  while IFS= read -r f; do files+=("$f"); done < <(find "$PLUGINS_DIR" -maxdepth 1 -name '*.yaml' | sort)
  [[ ${#files[@]} -gt 0 ]] || fail "no plugin files in $PLUGINS_DIR"

  local f
  for f in "${files[@]}"; do check_entry "$f"; done
  validate_entries "${files[@]}"
  build "${files[@]}"
  validate_catalog
  echo "wrote $OUT (${#files[@]} plugins)"
}

main "$@"
