#!/usr/bin/env bash
# Deploy the kvm GNOME Shell extensions from source to the running shell.
#
# The source of truth lives *here* (version-controlled, reviewable, diffable).
# This script copies it to the directory GNOME imports from and enables it, so
# editing the repo and running this script is the whole loop:
#
#     extensions/<uuid>/   --(deploy.sh)-->   ~/.local/share/gnome-shell/extensions/<uuid>/
#
# Usage:
#   scripts/deploy-extension.sh                 deploy every extension in extensions/
#   scripts/deploy-extension.sh kvm-cursor@mycc deploy one (by uuid)
#   scripts/deploy-extension.sh --remove <uuid> uninstall it (disable + delete + forget)
#
# A running shell never re-reads an already-imported extension: after a fresh
# deploy or a code change you must reload the session (log out / log in, or
# Alt+F2 `r` on X11). The script says so when it deploys.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_ROOT="$(cd "$HERE/.." && pwd)/extensions"
DEST_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}/gnome-shell/extensions"

die() { echo "deploy-extension: $*" >&2; exit 1; }

# enabled-extensions is a GVariant string-array. Edit it textually so we never
# clobber unrelated entries (gsettings merge for arrays is fragile).
enable_uuid() {
    local uuid="$1"
    local cur
    cur="$(gsettings get org.gnome.shell enabled-extensions)"
    if [[ "$cur" == *"'$uuid'"* ]]; then
        echo "  already enabled"
        return
    fi
    local new
    new="$(printf '%s' "$cur" | sed -e "s/^\[\]/['$uuid']/" -e "s/\]$/, '$uuid']/")"
    gsettings set org.gnome.shell enabled-extensions "$new"
    echo "  enabled in org.gnome.shell enabled-extensions"
}

disable_uuid() {
    local uuid="$1"
    local cur
    cur="$(gsettings get org.gnome.shell enabled-extensions)"
    [[ "$cur" == *"'$uuid'"* ]] || return 0
    local new
    new="$(printf '%s' "$cur" | sed -e "s/'$uuid', //" -e "s/, '$uuid'//" -e "s/\['$uuid'\]/[]/")"
    gsettings set org.gnome.shell enabled-extensions "$new"
    echo "  disabled"
}

deploy_one() {
    local uuid="$1"
    local src="$SRC_ROOT/$uuid"
    local dst="$DEST_ROOT/$uuid"
    [[ -f "$src/metadata.json" ]] || die "no such extension: $src"
    # metadata.json declares the uuid; a mismatched directory name silently
    # fails to load, so check it here rather than debugging gnome-shell later.
    local declared
    declared="$(sed -n 's/.*"uuid"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$src/metadata.json")"
    [[ "$declared" == "$uuid" ]] || die "$src/metadata.json declares uuid '$declared', not '$uuid'"

    echo "deploying $uuid"
    mkdir -p "$dst"
    # --checksum: leave untouched files alone so a running shell's mtime-based
    # caches are not needlessly invalidated.
    rsync -a --checksum --delete "$src"/ "$dst"/
    if command -v glib-compile-schemas >/dev/null && [[ -d "$dst/schemas" ]]; then
        glib-compile-schemas "$dst/schemas"
    fi
    echo "  -> $dst"
    enable_uuid "$uuid"
}

remove_one() {
    local uuid="$1"
    echo "removing $uuid"
    disable_uuid "$uuid"
    rm -rf "${DEST_ROOT:?}/$uuid"
    echo "  deleted from $DEST_ROOT"
}

if [[ "${1:-}" == "--remove" ]]; then
    [[ -n "${2:-}" ]] || die "--remove needs a uuid"
    remove_one "$2"
    exit 0
fi

[[ -d "$SRC_ROOT" ]] || die "no extensions/ directory at $SRC_ROOT"

if [[ -n "${1:-}" ]]; then
    deploy_one "$1"
    echo
    echo "Restart the shell to load it: log out / log in (or Alt+F2, 'r', Enter on X11)."
    exit 0
fi

for d in "$SRC_ROOT"/*/; do
    deploy_one "$(basename "$d")"
done
echo
echo "Restart the shell to load changes: log out / log in (or Alt+F2, 'r', Enter on X11)."
