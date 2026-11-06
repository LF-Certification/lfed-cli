#!/usr/bin/env bash
set -euo pipefail

# Release the host CLI or the SDK module in two steps.
#
#   scripts/create-release.sh [--dry-run] cli|sdk
#       Writes the changelog for the next version and commits it. The next
#       version comes from the Conventional Commits since the last tag.
#       Push the commit, or open a pull request when main is protected.
#
#   scripts/create-release.sh tag cli|sdk
#       Run on the merged commit: tags HEAD with the version at the top of
#       the changelog. HEAD MUST be on origin/main, so the tag is never on
#       a squashed-away commit. Push with `git push origin <tag>`.
#
# cli: tag vX.Y.Z, changelog CHANGELOG.md, commits outside sdk/.
# sdk: tag sdk/vX.Y.Z, changelog sdk/CHANGELOG.md, commits under sdk/.
# A v* tag runs release.yml. An sdk/v* tag publishes through the Go proxy.

main() {
    local dry_run=false mode=changelog component=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --dry-run) dry_run=true ;;
            tag) mode=tag ;;
            cli|sdk) component="$1" ;;
            *) die "unknown argument $1" ;;
        esac
        shift
    done
    [ -n "$component" ] || die "usage: $0 [--dry-run] [tag] cli|sdk"

    local tag_prefix changelog
    local -a paths
    case "$component" in
        cli)
            tag_prefix="v"
            changelog="CHANGELOG.md"
            paths=(--exclude-path "sdk/**")
            ;;
        sdk)
            tag_prefix="sdk/v"
            changelog="sdk/CHANGELOG.md"
            paths=(--include-path "sdk/**")
            ;;
    esac
    # --tag-pattern is an unanchored regex. Anchor it so v* does not match sdk/v*.
    local tag_pattern="^${tag_prefix}[0-9]"

    case "$mode" in
        changelog) changelog "$dry_run" "$tag_prefix" "$changelog" "$tag_pattern" "${paths[@]}" ;;
        tag) tag_head "$tag_prefix" "$changelog" ;;
    esac
}

changelog() {
    local dry_run="$1" tag_prefix="$2" changelog="$3" tag_pattern="$4"
    shift 4
    local -a paths=("$@")

    local next
    if [ -z "$(git tag -l "${tag_prefix}*")" ]; then
        next="${tag_prefix}${INITIAL_VERSION:-0.1.0}"
    else
        next=$(git cliff --bumped-version "${paths[@]}" --tag-pattern "$tag_pattern")
    fi
    [ -n "$next" ] || die "git-cliff did not propose a version"

    if [ "$dry_run" = true ]; then
        echo "Next version: $next"
        echo ""
        git cliff --unreleased --tag "$next" "${paths[@]}" --tag-pattern "$tag_pattern" --strip header
        return 0
    fi

    git diff-index --quiet HEAD -- || die "the working tree has uncommitted changes"

    git cliff --tag "$next" "${paths[@]}" --tag-pattern "$tag_pattern" --output "$changelog"
    git add "$changelog"
    git commit -m "chore(release): release $next"

    echo "Updated $changelog for $next."
    echo "Merge this commit into main, then run: $0 tag ${component}"
}

tag_head() {
    local tag_prefix="$1" changelog="$2"
    local version tag
    version=$(sed -n 's/^## \[\([0-9][^]]*\)\].*/\1/p' "$changelog" | head -n 1)
    [ -n "$version" ] || die "no version heading in $changelog"
    tag="${tag_prefix}${version}"

    git diff-index --quiet HEAD -- || die "the working tree has uncommitted changes"
    git fetch -q origin main
    git merge-base --is-ancestor HEAD origin/main || die "HEAD is not on origin/main; check out the merged commit"
    [ -z "$(git tag -l "$tag")" ] || die "tag $tag exists"
    git tag "$tag" -m "Release $tag"

    echo "Created tag $tag on $(git rev-parse --short HEAD)."
    echo "Run 'git push origin $tag' to publish."
}

die() { echo "create-release.sh: $*" >&2; exit 1; }

main "$@"
