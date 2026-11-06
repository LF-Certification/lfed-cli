#!/usr/bin/env bash
set -euo pipefail

main() {
    REPO="${LFED_REPO:-LF-Certification/lfed-cli}"
    TAG_PREFIX="v"
    INSTALL_DIR="${LFED_INSTALL_DIR:-$HOME/.local/bin}"

    die() { echo "install.sh: $*" >&2; exit 1; }
    need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

    need curl
    need tar

    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) die "unsupported OS: $(uname -s)" ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) die "unsupported architecture: $(uname -m)" ;;
    esac

    auth=()
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
    fi

    api() {
        curl -fsSL ${auth[@]+"${auth[@]}"} -H "Accept: application/vnd.github+json" "https://api.github.com/repos/${REPO}${1:+/$1}"
    }

    latest_version() {
        api "releases/latest" |
            { grep -o "\"tag_name\": *\"${TAG_PREFIX}[0-9][^\"]*\"" || true; } |
            sed -e "s|.*${TAG_PREFIX}||" -e 's|"$||'
    }

    # GitHub redirects a transferred repository. The signing identity holds the
    # name at signing time. Resolve the canonical name so the identity check
    # follows a transfer.
    canonical_repo() {
        { api "" | grep -o '"full_name": *"[^"]*"' || true; } | sed -e 's|.*: *"||' -e 's|"$||' | head -n1
    }

    asset_url() {
        { grep -o "\"browser_download_url\": *\"[^\"]*/$1\"" "${tmp}/release.json" || true; } | sed -e 's|.*: *"||' -e 's|"$||' | head -n1
    }

    version="${LFED_VERSION:-}"
    if [ -z "$version" ]; then
        version="$(latest_version)"
        [ -n "$version" ] || die "no ${TAG_PREFIX}* release found in ${REPO}"
    fi
    version="${version#v}"
    tag="${TAG_PREFIX}${version}"
    archive="lfed_${version}_${os}_${arch}.tar.gz"

    tmp="$(mktemp -d)"
    staged=""
    trap 'rm -rf "$tmp" "$staged"' EXIT

    api "releases/tags/${tag}" > "${tmp}/release.json" || die "release ${tag} not found in ${REPO}"
    signer_repo="$(canonical_repo)"
    [ -n "$signer_repo" ] || signer_repo="$REPO"
    archive_url="$(asset_url "$archive")"
    checksums_url="$(asset_url checksums.txt)"
    [ -n "$archive_url" ] || die "release ${tag} has no asset ${archive}"
    [ -n "$checksums_url" ] || die "release ${tag} has no checksums.txt"

    echo "Downloading lfed ${version} (${os}/${arch})..."
    curl -fsSL -o "${tmp}/${archive}" "$archive_url" || die "download failed: ${archive_url}"
    curl -fsSL -o "${tmp}/checksums.txt" "$checksums_url" || die "download failed: ${checksums_url}"

    # LFED_REQUIRE_SIGNATURE=1 makes a missing cosign an error. Without
    # cosign the archive is checked against checksums.txt only, which comes
    # from the same origin as the archive.
    if command -v cosign >/dev/null 2>&1; then
        bundle_url="$(asset_url checksums.txt.sigstore.json)"
        [ -n "$bundle_url" ] || die "release ${tag} has no checksums.txt.sigstore.json; cannot verify the signature. Set LFED_VERSION to a signed release."
        curl -fsSL -o "${tmp}/checksums.txt.sigstore.json" "$bundle_url" || die "download failed: ${bundle_url}"
        if ! cosign_out="$(cosign verify-blob \
            --bundle "${tmp}/checksums.txt.sigstore.json" \
            --certificate-identity "https://github.com/${signer_repo}/.github/workflows/release.yml@refs/tags/${tag}" \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com \
            "${tmp}/checksums.txt" 2>&1)"; then
            echo "$cosign_out" >&2
            die "signature verification of checksums.txt failed for ${tag}"
        fi
        echo "Verified the signature of checksums.txt."
    elif [ "${LFED_REQUIRE_SIGNATURE:-}" = 1 ]; then
        die "cosign is required when LFED_REQUIRE_SIGNATURE=1"
    else
        echo "cosign not found: the signature of checksums.txt was not checked."
    fi

    need_sha() {
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum "$1" | cut -d' ' -f1
        else
            shasum -a 256 "$1" | cut -d' ' -f1
        fi
    }
    expected="$({ grep " ${archive}\$" "${tmp}/checksums.txt" || true; } | cut -d' ' -f1)"
    [ -n "$expected" ] || die "${archive} is not listed in checksums.txt"
    actual="$(need_sha "${tmp}/${archive}")"
    [ "$expected" = "$actual" ] || die "checksum mismatch for ${archive}: expected ${expected}, got ${actual}"

    tar -xzf "${tmp}/${archive}" -C "$tmp" lfed
    mkdir -p "$INSTALL_DIR"
    # Stage in the target directory so the final step is one rename.
    staged="$(mktemp "${INSTALL_DIR}/.lfed.XXXXXX")"
    install -m 0755 "${tmp}/lfed" "$staged"
    mv -f "$staged" "${INSTALL_DIR}/lfed"

    echo "Installed ${INSTALL_DIR}/lfed"
    "${INSTALL_DIR}/lfed" version
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *) echo "Add ${INSTALL_DIR} to your PATH." ;;
    esac
}

main "$@"
