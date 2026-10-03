#!/bin/sh
# install.sh: sets up perch and its runtime dependencies, or upgrades an
# installed perch (--upgrade).
#
# Usage:
#   ./install.sh [--skip-agents] [--skip-build] [--prefix=DIR]
#   ./install.sh --upgrade[=release|source] [--prefix=DIR]
#
# --upgrade replaces the installed perch with the newest stable release.
# Release mode (the default outside a git checkout) downloads the GitHub
# release binary and verifies it against SHA256SUMS. Source mode (the default
# inside a git checkout) checks out the newest v* tag and builds it. Both
# keep the previous binary as perch.prev and refresh the desktop entry.
#
# This script uses POSIX sh only, with no bash-specific syntax. It passes
# `shellcheck -s sh` with no findings.
set -e

# ---------------------------------------------------------------------------
# Repo root: found from the script location, not the current directory.
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

# ---------------------------------------------------------------------------
# Flags
# ---------------------------------------------------------------------------
SKIP_AGENTS=0
SKIP_BUILD=0
INSTALL_PREFIX=""
# UPGRADE is empty for a normal install, or one of auto, release, source.
UPGRADE=""

# The GitHub repository that publishes the release binaries.
PERCH_REPO_URL="https://github.com/miniature-pug/perch"
PERCH_RAW_URL="https://raw.githubusercontent.com/miniature-pug/perch"

# Remember where the user ran the script, so a relative --prefix resolves
# against that directory and not against the repo root.
START_DIR="$(pwd)"

for arg in "$@"; do
  case "$arg" in
    --skip-agents) SKIP_AGENTS=1 ;;
    --skip-build)  SKIP_BUILD=1  ;;
    --prefix=*)    INSTALL_PREFIX="${arg#--prefix=}" ;;
    --upgrade)     UPGRADE=auto ;;
    --upgrade=release | --upgrade=source) UPGRADE="${arg#--upgrade=}" ;;
    --upgrade=*)
      printf 'error: --upgrade takes release or source, not %s\n' "${arg#--upgrade=}" >&2
      exit 2
      ;;
    -h | --help)
      sed -n '5,13p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      printf 'error: unknown flag: %s\n' "$arg" >&2
      exit 2
      ;;
  esac
done

# --prefix is the directory that receives the perch binary (DIR/perch). Make
# it absolute now: the script later cd's into the repo root.
case "$INSTALL_PREFIX" in
  "" | /*) ;;
  *) INSTALL_PREFIX="${START_DIR}/${INSTALL_PREFIX}" ;;
esac

if [ -n "$UPGRADE" ] && [ "$SKIP_BUILD" = "1" ]; then
  printf 'error: --upgrade and --skip-build cannot be combined\n' >&2
  exit 2
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
die() { printf 'error: %s\n' "$1" >&2; exit 1; }

# version_ge A B: succeeds when dotted version A >= B (numeric, per component).
version_ge() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    na = split(a, x, "."); nb = split(b, y, ".");
    n = (na > nb) ? na : nb;
    for (i = 1; i <= n; i++) {
      xi = x[i] + 0; yi = y[i] + 0;
      if (xi > yi) exit 0;
      if (xi < yi) exit 1;
    }
    exit 0
  }'
}

# run_remote_installer NAME URL: download an installer script to a temp file,
# check that the download succeeded and is non-empty, then run it. A plain
# "curl | sh" would run an empty script (and report success) when the download
# fails, because POSIX sh has no pipefail.
# Returns non-zero (after printing a [warn]) on failure instead of aborting:
# an agent CLI is optional for the perch build, and the installer URLs can
# move, so a failure here must not stop the perch install.
run_remote_installer() {
  _name="$1"
  _url="$2"
  _script="$(mktemp "${TMPDIR:-/tmp}/perch-install-${_name}.XXXXXX")" || {
    printf '[warn]  Cannot create a temp file for the %s installer; skipping it\n' "$_name"
    return 1
  }
  if ! curl -fsSL -o "$_script" "$_url" || [ ! -s "$_script" ]; then
    rm -f "$_script"
    printf '[warn]  Failed to download the %s installer from %s; skipping it\n' "$_name" "$_url"
    printf '        Install %s manually, or re-run with --skip-agents.\n' "$_name"
    return 1
  fi
  # These installers are bash scripts. Prefer bash over a minimal /bin/sh.
  if command -v bash >/dev/null 2>&1; then
    _runner=bash
  else
    _runner="sh"
  fi
  if ! "$_runner" "$_script"; then
    rm -f "$_script"
    printf '[warn]  The %s installer failed; continuing without it\n' "$_name"
    return 1
  fi
  rm -f "$_script"
}

# Read a pinned version from .tool-versions.
tool_version() {
  grep "^$1 " "${REPO_ROOT}/.tool-versions" | awk '{print $2}'
}

# Return the first dotted version number a CLI tool reports. Tools format
# --version differently ("2.1.288 (Claude Code)", "1.15.12"), so match the
# number instead of taking a fixed field.
installed_version() {
  "$1" --version 2>/dev/null | grep -Eo '[0-9]+(\.[0-9]+)+' | head -n 1
}

# run_bounded CMD...: run CMD with a 10s limit when timeout(1) exists.
run_bounded() {
  if command -v timeout >/dev/null 2>&1; then
    timeout 10 "$@"
  else
    "$@"
  fi
}

# ---------------------------------------------------------------------------
# Desktop integration (Linux): the hicolor icon and the perch.desktop entry.
# GNOME on Wayland ignores the window's own icon: the dock and the app
# switcher show the icon of the .desktop entry that matches the window
# (app_id and WM_CLASS "perch"), or a generic icon when there is none.
# ---------------------------------------------------------------------------

# supports_install_desktop BIN: succeeds when BIN has the install-desktop
# subcommand. An unknown argument makes perch treat it as a project path;
# a path that does not exist prints the usage and exits without opening a
# window, so grep the usage. (The probe path must not contain the
# subcommand name: perch echoes it back in its error.)
supports_install_desktop() {
  "$1" /nonexistent/.perch-usage-probe 2>&1 | grep -q 'perch install-desktop'
}

# is_png FILE: succeeds when FILE starts with the PNG signature.
is_png() {
  [ -s "$1" ] && [ "$(dd if="$1" bs=1 skip=1 count=3 2>/dev/null)" = "PNG" ]
}

# icon_source: set ICON_SRC to the perch icon to install. A checkout has
# app/appicon.png next to this script. A standalone copy of the script (the
# release-mode upgrade) fetches the icon of the release tag it installs,
# best effort: the icon is cosmetic, so it is not checksummed, only checked
# to be a PNG. Call it directly, never in $(...): ICON_TMP must reach
# on_exit in this shell.
icon_source() {
  ICON_SRC=""
  if [ -f "${REPO_ROOT}/app/appicon.png" ]; then
    ICON_SRC="${REPO_ROOT}/app/appicon.png"
    return 0
  fi
  [ -n "${RELEASE_TAG:-}" ] || return 1
  if [ -z "${ICON_TMP:-}" ]; then
    ICON_TMP="$(mktemp "${TMPDIR:-/tmp}/perch-icon.XXXXXX")" || return 1
  fi
  if curl -fsSL -o "$ICON_TMP" "${PERCH_RAW_URL}/${RELEASE_TAG}/app/appicon.png" 2>/dev/null \
    && is_png "$ICON_TMP"; then
    ICON_SRC="$ICON_TMP"
    return 0
  fi
  return 1
}

# write_desktop_entry_inline BIN: the fallback for a perch binary without
# the install-desktop subcommand (v0.1.0 and older). It writes the same files
# the subcommand writes, each through a temp file and a rename, then
# refreshes the caches.
write_desktop_entry_inline() {
  if ! icon_source; then
    printf '[warn]  no perch icon available (no app/appicon.png next to this script, and the\n'
    printf '        download failed); skipping the desktop entry. Run "perch install-desktop"\n'
    printf '        with a perch that has the subcommand, or ./install.sh from a clone.\n'
    return 0
  fi
  _data="${XDG_DATA_HOME:-}"
  case "$_data" in
    /*) ;;
    *) _data="${HOME}/.local/share" ;;
  esac
  # Exec quoting per the Desktop Entry spec: inside double quotes escape \ " `
  # and $ with a backslash, then double every backslash again for the
  # string-value escape layer. (desktop_entry already refused a path with %.)
  _exec="$(printf '%s' "$1" \
    | sed -e 's/\\/\\\\/g' -e 's/["`$]/\\&/g' -e 's/\\/\\\\/g')"
  _icon_dir="${_data}/icons/hicolor/512x512/apps"
  _apps_dir="${_data}/applications"
  mkdir -p "$_icon_dir" "$_apps_dir" || return 1
  cp "$ICON_SRC" "${_icon_dir}/.perch.png.$$" \
    && chmod 0644 "${_icon_dir}/.perch.png.$$" \
    && mv -f "${_icon_dir}/.perch.png.$$" "${_icon_dir}/perch.png" || return 1
  cat > "${_apps_dir}/.perch.desktop.$$" <<DESKTOP || return 1
[Desktop Entry]
Type=Application
Name=perch
Comment=Cockpit for AI coding agents
Exec="${_exec}"
Icon=perch
Terminal=false
Categories=Development;
StartupWMClass=perch
DESKTOP
  chmod 0644 "${_apps_dir}/.perch.desktop.$$" \
    && mv -f "${_apps_dir}/.perch.desktop.$$" "${_apps_dir}/perch.desktop" || return 1
  # Best effort: GNOME also notices the files on its own, at the latest on
  # the next login. As xdg-icon-resource does, bump the theme dir's mtime and
  # refresh only an existing icon cache. Never create one: GTK checks a
  # cache against the theme dir's mtime only, so a new user-level cache
  # would hide icons other apps add to its size dirs later.
  touch "${_data}/icons/hicolor" 2>/dev/null || true
  if [ -f "${_data}/icons/hicolor/icon-theme.cache" ] \
    && command -v gtk-update-icon-cache >/dev/null 2>&1; then
    run_bounded gtk-update-icon-cache -f -t "${_data}/icons/hicolor" >/dev/null 2>&1 || true
  fi
  if command -v update-desktop-database >/dev/null 2>&1; then
    run_bounded update-desktop-database "$_apps_dir" >/dev/null 2>&1 || true
  fi
  printf '[ok]    desktop entry installed at %s/perch.desktop\n' "$_apps_dir"
}

# desktop_entry BIN: install the icon and perch.desktop (Exec=BIN) for the
# user who ran the script. Never fails the script.
desktop_entry() {
  _bin="$1"
  if [ "$GOOS" != "linux" ]; then
    return 0
  fi
  if [ ! -x "$_bin" ]; then
    printf '[skip] desktop entry (no perch binary at %s)\n' "$_bin"
    return 0
  fi
  case "$_bin" in
    *%*)
      # GLib resolves Exec's program before it expands %%, so GNOME drops
      # an entry for such a path.
      printf '[warn]  skipping the desktop entry: %s contains %%, which GNOME cannot launch\n' "$_bin"
      printf '        from a .desktop entry. Install perch in a directory without %% in its path.\n'
      return 0
      ;;
  esac
  # Under sudo, HOME is root's home: an entry written there is invisible to
  # the desktop user. Write it as the user who ran sudo instead.
  _as_user=""
  if [ "$(id -u)" = "0" ]; then
    if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
      _as_user="$SUDO_USER"
    else
      printf '[warn]  running as root: the desktop entry goes under %s, so only root sees it\n' "$HOME"
    fi
  fi
  if supports_install_desktop "$_bin"; then
    if [ -n "$_as_user" ]; then
      if sudo -u "$_as_user" -H "$_bin" install-desktop; then
        printf '[ok]    desktop entry installed for %s\n' "$_as_user"
      else
        printf '[warn]  could not install the desktop entry for %s\n' "$_as_user"
        printf '        Run "%s install-desktop" as %s, without sudo.\n' "$_bin" "$_as_user"
      fi
    elif ! "$_bin" install-desktop; then
      printf '[warn]  "%s install-desktop" failed; the dock may show a generic icon\n' "$_bin"
    fi
    return 0
  fi
  if [ -n "$_as_user" ]; then
    printf '[warn]  skipping the desktop entry: under sudo it would land in root'"'"'s home\n'
    printf '        Re-run as %s without sudo: ./install.sh --skip-agents --skip-build --prefix=%s\n' \
      "$_as_user" "$(dirname "$_bin")"
    return 0
  fi
  write_desktop_entry_inline "$_bin" \
    || printf '[warn]  could not write the desktop entry; the dock may show a generic icon\n'
}

# ---------------------------------------------------------------------------
# Upgrade helpers (--upgrade)
# ---------------------------------------------------------------------------

# perch_version BIN: the version BIN reports ("v1.2.3"), or nothing.
perch_version() {
  if [ -x "$1" ]; then
    "$1" version 2>/dev/null | awk 'NR == 1 && $1 == "perch" { print $2 }'
  fi
}

# version_core V: the numeric core of a release version or a describe
# string built on one ("v1.2.3-rc1" -> "1.2.3"), or nothing for anything
# else. "git describe --always" in a clone without tags gives a bare hash
# ("527c10b"), and a build without ldflags reports "dev": neither is a
# version, and a hash must never be read as a number.
version_core() {
  case "$1" in
    v[0-9]*.[0-9]*) ;;
    *) return 0 ;;
  esac
  _vc="$(printf '%s' "$1" | sed -e 's/^v//' -e 's/[^0-9.].*$//')"
  case "$_vc" in
    [0-9]*.[0-9]*) printf '%s' "$_vc" ;;
  esac
}

# is_release_version V: succeeds when V is (or builds on) a vN.N tag.
is_release_version() {
  [ -n "$(version_core "$1")" ]
}

# version_newer A B: succeeds when A's numeric core is strictly newer than
# B's. Fails when either is not a release version.
version_newer() {
  _va="$(version_core "$1")"
  _vb="$(version_core "$2")"
  [ -n "$_va" ] && [ -n "$_vb" ] && version_ge "$_va" "$_vb" && ! version_ge "$_vb" "$_va"
}

# in_checkout: succeeds when this script sits at the root of a perch git
# checkout.
in_checkout() {
  [ -f "${REPO_ROOT}/cmd/perch/main.go" ] && [ -f "${REPO_ROOT}/go.mod" ] \
    && command -v git >/dev/null 2>&1 \
    && git -C "$REPO_ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1
}

# physical_dir DIR: DIR with symlinks resolved, or DIR itself.
physical_dir() {
  (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"
}

# resolve_upgrade_target: pick the binary to replace. --prefix wins;
# otherwise the perch first on PATH; otherwise the normal install default.
resolve_upgrade_target() {
  if [ -z "$INSTALL_PREFIX" ]; then
    _cur="$(command -v perch 2>/dev/null || true)"
    case "$_cur" in
      /*) INSTALL_PREFIX="$(dirname "$_cur")" ;;
      *)
        if [ -w /usr/local/bin ]; then
          INSTALL_PREFIX="/usr/local/bin"
        else
          INSTALL_PREFIX="${HOME}/.local/bin"
        fi
        ;;
    esac
  fi
  mkdir -p "$INSTALL_PREFIX" || die "Cannot create ${INSTALL_PREFIX}"
  if [ ! -w "$INSTALL_PREFIX" ]; then
    die "cannot write to ${INSTALL_PREFIX}. Re-run with --prefix=DIR, or with sudo for a system-wide install."
  fi
  TARGET="${INSTALL_PREFIX}/perch"
  OLD_VERSION="$(perch_version "$TARGET")"
}

# make_upgrade_tmp: a temp directory inside the install prefix, so the final
# mv is a rename on one filesystem. Removed on exit.
make_upgrade_tmp() {
  UPGRADE_TMP="$(mktemp -d "${INSTALL_PREFIX}/.perch-upgrade.XXXXXX")" \
    || die "cannot create a temp directory in ${INSTALL_PREFIX}"
}

# installed_rev: the commit the installed perch was built from, when its
# version is a bare hash ("527c10b" or "527c10b-dirty") this clone knows;
# otherwise HEAD.
installed_rev() {
  _h="${OLD_VERSION%-dirty}"
  case "$_h" in
    '' | *[!0-9a-f]*) ;;
    *)
      if git -C "$REPO_ROOT" rev-parse --verify -q "${_h}^{commit}" >/dev/null 2>&1; then
        printf '%s' "$_h"
        return 0
      fi
      ;;
  esac
  printf 'HEAD'
}

# exit_if_current TAG: stop when the installed perch is already TAG, or is a
# newer build (a later tag, or a source build ahead of TAG).
exit_if_current() {
  if [ "$OLD_VERSION" = "$1" ]; then
    printf '[ok]    perch %s at %s is the newest release; nothing to upgrade\n' "$1" "$TARGET"
    desktop_entry "$TARGET"
    exit 0
  fi
  if [ -z "$OLD_VERSION" ]; then
    return 0
  fi
  if is_release_version "$OLD_VERSION"; then
    case "$OLD_VERSION" in
      "$1"-[0-9]*-g*)
        printf '[ok]    perch %s at %s is ahead of the newest release %s; nothing to upgrade\n' \
          "$OLD_VERSION" "$TARGET" "$1"
        exit 0
        ;;
    esac
    if version_newer "$OLD_VERSION" "$1"; then
      printf '[ok]    perch %s at %s is newer than the newest release %s; nothing to upgrade\n' \
        "$OLD_VERSION" "$TARGET" "$1"
      exit 0
    fi
    return 0
  fi
  # Not a release version: a bare commit hash or "dev". Source mode asks git
  # whether that build (or, failing that, this checkout) already contains
  # the tag. Release mode cannot tell, so it upgrades.
  if [ "$UPGRADE" = "source" ]; then
    _rev="$(installed_rev)"
    if git -C "$REPO_ROOT" merge-base --is-ancestor "$1" "$_rev" 2>/dev/null; then
      if [ "$_rev" = "HEAD" ]; then
        printf '[ok]    this checkout already contains %s (installed perch reports "%s"); nothing to upgrade\n' \
          "$1" "$OLD_VERSION"
        printf '        Run ./install.sh (without --upgrade) to install this checkout as it is.\n'
      else
        printf '[ok]    perch %s at %s already contains %s; nothing to upgrade\n' \
          "$OLD_VERSION" "$TARGET" "$1"
      fi
      exit 0
    fi
    return 0
  fi
  printf '[upgrade] installed perch reports "%s", which is not a release version; installing %s\n' \
    "$OLD_VERSION" "$1"
}

# smoke_test BIN: BIN must run and report NEW_TAG. Sets NEW_VERSION.
smoke_test() {
  if ! _out="$("$1" version 2>&1)"; then
    printf '%s\n' "$_out" >&2
    die "the new perch binary does not run (are the WebKit2GTK 4.1 and GTK3 libraries installed?). ${TARGET} is unchanged."
  fi
  NEW_VERSION="$(printf '%s\n' "$_out" | awk 'NR == 1 && $1 == "perch" { print $2 }')"
  if [ "$NEW_VERSION" != "$NEW_TAG" ]; then
    die "the new perch binary reports version '${NEW_VERSION}', expected ${NEW_TAG}. ${TARGET} is unchanged."
  fi
}

# swap_in NEW: keep the current binary as perch.prev, then rename NEW over
# perch. A running perch keeps its old inode, so the swap is safe while it
# runs.
swap_in() {
  if [ -e "$TARGET" ]; then
    ln -f "$TARGET" "${TARGET}.prev" 2>/dev/null \
      || cp -p "$TARGET" "${TARGET}.prev" \
      || die "cannot keep the old binary as ${TARGET}.prev. ${TARGET} is unchanged."
  fi
  chmod 0755 "$1"
  mv -f "$1" "$TARGET" || die "cannot move the new binary into ${TARGET}"
}

# finish_upgrade: report old -> new, and warn about anything that would keep
# the user on the old binary.
finish_upgrade() {
  printf '[ok]    perch %s -> %s at %s\n' "${OLD_VERSION:-(none)}" "$NEW_VERSION" "$TARGET"
  if [ -e "${TARGET}.prev" ]; then
    printf '        The previous binary is kept at %s.prev. To roll back: mv -f %s.prev %s\n' \
      "$TARGET" "$TARGET" "$TARGET"
  fi
  if command -v pgrep >/dev/null 2>&1 && pgrep -x perch >/dev/null 2>&1; then
    printf '[warn]  perch is running. Quit it (close every perch window), then start it again.\n'
    printf '        Starting perch while the old one runs only raises the old window.\n'
  fi
  _first="$(command -v perch 2>/dev/null || true)"
  if [ -z "$_first" ]; then
    printf '[warn]  %s is not on your PATH. Add it to run "perch" by name.\n' "$INSTALL_PREFIX"
  elif [ "$(physical_dir "$(dirname "$_first")")" != "$(physical_dir "$INSTALL_PREFIX")" ]; then
    printf '[warn]  another perch comes first on your PATH: %s (%s)\n' \
      "$_first" "$(perch_version "$_first")"
    printf '        "perch" runs that one, not %s. Remove it or reorder PATH.\n' "$TARGET"
  fi
}

# upgrade_release: replace TARGET with the latest GitHub release binary.
upgrade_release() {
  if [ "$GOOS" != "linux" ]; then
    die "release binaries exist only for linux. Use --upgrade=source from a checkout."
  fi
  command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required to verify the download"
  # /releases/latest redirects to /releases/tag/<newest stable tag>.
  _latest="$(curl -sSfLI -o /dev/null -w '%{url_effective}' "${PERCH_REPO_URL}/releases/latest")" \
    || die "cannot reach ${PERCH_REPO_URL}/releases/latest"
  NEW_TAG="${_latest##*/}"
  case "$NEW_TAG" in
    v[0-9]*) ;;
    *) die "no published release found (${PERCH_REPO_URL}/releases/latest resolved to ${_latest})" ;;
  esac
  # The inline desktop-entry fallback fetches this tag's icon.
  RELEASE_TAG="$NEW_TAG"
  printf '[upgrade] newest release: %s; installed: %s\n' "$NEW_TAG" "${OLD_VERSION:-(none)}"
  exit_if_current "$NEW_TAG"

  _asset="perch-linux-${ARCH}"
  _base="${PERCH_REPO_URL}/releases/download/${NEW_TAG}"
  make_upgrade_tmp
  printf '[download] %s/%s\n' "$_base" "$_asset"
  if ! curl -fsSL -o "${UPGRADE_TMP}/${_asset}" "${_base}/${_asset}" \
    || [ ! -s "${UPGRADE_TMP}/${_asset}" ]; then
    die "failed to download ${_asset} for ${NEW_TAG}. ${TARGET} is unchanged."
  fi
  if ! curl -fsSL -o "${UPGRADE_TMP}/SHA256SUMS" "${_base}/SHA256SUMS" \
    || [ ! -s "${UPGRADE_TMP}/SHA256SUMS" ]; then
    die "failed to download SHA256SUMS for ${NEW_TAG}. ${TARGET} is unchanged."
  fi
  # Check only this asset's line: the file also lists the other architecture.
  awk -v f="$_asset" '$2 == f || $2 == "*" f' "${UPGRADE_TMP}/SHA256SUMS" \
    > "${UPGRADE_TMP}/SHA256SUMS.asset"
  if [ ! -s "${UPGRADE_TMP}/SHA256SUMS.asset" ]; then
    die "SHA256SUMS for ${NEW_TAG} has no entry for ${_asset}. ${TARGET} is unchanged."
  fi
  if ! (cd "$UPGRADE_TMP" && sha256sum -c SHA256SUMS.asset >/dev/null 2>&1); then
    die "checksum mismatch for ${_asset} ${NEW_TAG}. ${TARGET} is unchanged."
  fi
  printf '[ok]    %s matches SHA256SUMS\n' "$_asset"
  chmod 0755 "${UPGRADE_TMP}/${_asset}"
  smoke_test "${UPGRADE_TMP}/${_asset}"
  swap_in "${UPGRADE_TMP}/${_asset}"
  desktop_entry "$TARGET"
  finish_upgrade
}

# upgrade_source_resolve: in a clean checkout this user owns, fetch tags and
# pick the newest stable v* tag. Exits when there is nothing to upgrade.
# Nothing in the checkout changes yet.
upgrade_source_resolve() {
  # As root (sudo), git, npm and go would leave root-owned objects, refs and
  # build output in a clone another user owns, and break that user's next
  # fetch. Refuse instead of half-working. (Checked before any git call:
  # git itself may refuse a repository another user owns.)
  if [ "$(id -u)" = "0" ] && [ -e "${REPO_ROOT}/.git" ]; then
    _owner="$(stat -c %u "${REPO_ROOT}/.git" 2>/dev/null || stat -f %u "${REPO_ROOT}/.git" 2>/dev/null || true)"
    if [ -n "$_owner" ] && [ "$_owner" != "0" ]; then
      printf 'error: refusing --upgrade=source as root: the checkout at %s belongs to uid %s,\n' "$REPO_ROOT" "$_owner" >&2
      printf 'and git, npm and go would leave root-owned files in it. Either:\n' >&2
      printf "  - re-run without sudo: ./install.sh --upgrade --prefix=\"\$HOME/.local/bin\"\n" >&2
      printf '  - or upgrade from the release binary: sudo ./install.sh --upgrade=release\n' >&2
      exit 1
    fi
  fi
  in_checkout || die "--upgrade=source must run from a perch git checkout; ${REPO_ROOT} is not one"
  if [ -n "$(git -C "$REPO_ROOT" status --porcelain --untracked-files=no)" ]; then
    die "the checkout at ${REPO_ROOT} has uncommitted changes. Commit or stash them, then re-run."
  fi
  printf '[upgrade] fetching tags\n'
  git -C "$REPO_ROOT" fetch --tags --quiet || die "git fetch --tags failed in ${REPO_ROOT}"
  # Newest stable tag: skip pre-releases (a "-" suffix), newest by version.
  NEW_TAG="$(git -C "$REPO_ROOT" tag -l 'v[0-9]*' | grep -v -- '-' | sort -V | tail -n 1)"
  [ -n "$NEW_TAG" ] || die "no v* release tag found in ${REPO_ROOT}"
  printf '[upgrade] newest release tag: %s; installed: %s\n' "$NEW_TAG" "${OLD_VERSION:-(none)}"
  exit_if_current "$NEW_TAG"
}

# upgrade_source_checkout: check out NEW_TAG, after the prerequisite checks
# passed. RESTORE_REF makes on_exit return the checkout to the original
# branch (or commit) if anything later fails.
upgrade_source_checkout() {
  if [ "$(git -C "$REPO_ROOT" rev-parse HEAD)" = "$(git -C "$REPO_ROOT" rev-parse "${NEW_TAG}^{commit}")" ]; then
    return 0
  fi
  _from="$(git -C "$REPO_ROOT" symbolic-ref --short -q HEAD || git -C "$REPO_ROOT" rev-parse HEAD)"
  git -C "$REPO_ROOT" checkout --quiet "$NEW_TAG" || die "cannot check out ${NEW_TAG}"
  RESTORE_REF="$_from"
  printf '[upgrade] checked out %s (was %s)\n' "$NEW_TAG" "$_from"
}

# on_exit: the one EXIT trap. It removes temp files, restores the tracked
# frontend/dist stub that a build overwrote, and, when a source upgrade
# fails after its checkout, returns the checkout to the original ref.
on_exit() {
  _rc=$?
  if [ -n "${UPGRADE_TMP:-}" ]; then
    rm -rf "$UPGRADE_TMP"
  fi
  if [ -n "${ICON_TMP:-}" ]; then
    rm -f "$ICON_TMP"
  fi
  if [ -n "${GO_TMPFILE:-}" ] || [ -n "${GO_STAGE_DIR:-}" ]; then
    $SUDO rm -rf "${GO_TMPFILE:-}" "${GO_STAGE_DIR:-}"
  fi
  if [ "${DIST_TOUCHED:-0}" = "1" ]; then
    git -C "$REPO_ROOT" checkout -- frontend/dist/index.html 2>/dev/null || true
  fi
  if [ "$_rc" -ne 0 ] && [ -n "${RESTORE_REF:-}" ]; then
    if git -C "$REPO_ROOT" checkout --quiet "$RESTORE_REF" 2>/dev/null; then
      printf '[upgrade] failed; returned the checkout to %s\n' "$RESTORE_REF" >&2
    else
      printf '[warn]  could not return the checkout to %s; run: git checkout %s\n' \
        "$RESTORE_REF" "$RESTORE_REF" >&2
    fi
  fi
}

# Determine whether to use sudo (never call sudo when already root).
if [ "$(id -u)" = "0" ]; then
  SUDO=""
else
  SUDO="sudo"
fi

trap on_exit EXIT
trap 'exit 130' INT TERM

# ---------------------------------------------------------------------------
# Architecture / OS detection
# ---------------------------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)          ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
  Linux)  GOOS="linux"  ;;
  Darwin) GOOS="darwin" ;;
  *) die "Unsupported OS: $OS" ;;
esac

# ---------------------------------------------------------------------------
# Package manager detection
# ---------------------------------------------------------------------------
PKG_MGR=""
if command -v apt-get >/dev/null 2>&1; then
  PKG_MGR="apt"
elif command -v dnf >/dev/null 2>&1; then
  PKG_MGR="dnf"
elif command -v brew >/dev/null 2>&1; then
  PKG_MGR="brew"
elif command -v pacman >/dev/null 2>&1; then
  PKG_MGR="pacman"
fi

APT_UPDATED=0

pkg_install() {
  case "$PKG_MGR" in
    apt)
      # Fresh Debian and Ubuntu images ship an empty package cache. Refresh
      # it once, before the first install, so package names resolve.
      if [ "$APT_UPDATED" -eq 0 ]; then
        $SUDO apt-get update -qq
        APT_UPDATED=1
      fi
      $SUDO apt-get install -y "$1"
      ;;
    dnf)    $SUDO dnf install -y "$1" ;;
    brew)   brew install "$1" ;;
    pacman) $SUDO pacman -S --noconfirm "$1" ;;
    *) die "No supported package manager found. Please install $1 manually." ;;
  esac
}

# ---------------------------------------------------------------------------
# Ensure curl is present. The Ubuntu base image does not include it by default.
# ---------------------------------------------------------------------------
if ! command -v curl >/dev/null 2>&1; then
  printf '[install] curl (required for downloads)\n'
  pkg_install curl
fi

# ---------------------------------------------------------------------------
# --upgrade: release mode replaces the binary here and exits. Source mode
# resolves the newest tag here, checks it out after the preflight below
# passes, then continues through the normal build (agents are left alone)
# into a temp file, and swaps it in at Step 6. If anything fails after the
# checkout, on_exit returns the clone to its original branch.
# ---------------------------------------------------------------------------
if [ "$UPGRADE" = "auto" ]; then
  # A clone is recognised by its files, not by asking git, so a clone git
  # distrusts (another user's, under sudo) still picks source mode and gets
  # its clear refusal rather than a silent switch to release mode.
  if [ -f "${REPO_ROOT}/cmd/perch/main.go" ] && [ -e "${REPO_ROOT}/.git" ]; then
    UPGRADE="source"
  else
    UPGRADE="release"
  fi
fi
if [ -n "$UPGRADE" ]; then
  resolve_upgrade_target
  printf '[upgrade] %s mode, target %s\n' "$UPGRADE" "$TARGET"
  if [ "$UPGRADE" = "release" ]; then
    upgrade_release
    exit 0
  fi
  upgrade_source_resolve
  SKIP_AGENTS=1
fi

# ---------------------------------------------------------------------------
# Preflight: fail on missing build prerequisites before any long download.
# Needs the webkit2gtk-4.1 and gtk+-3.0 dev libraries (Linux GUI build), and
# Node.js with npm to build the frontend that the production binary embeds.
# ---------------------------------------------------------------------------
if [ "$SKIP_BUILD" != "1" ]; then
  if [ "$OS" = "Linux" ]; then
    _webkit_ok=1
    if ! command -v pkg-config >/dev/null 2>&1; then
      _webkit_ok=0
    elif ! pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
      _webkit_ok=0
    elif ! pkg-config --exists gtk+-3.0 2>/dev/null; then
      _webkit_ok=0
    fi
    if [ "$_webkit_ok" = "0" ]; then
      printf 'error: the perch GUI build requires webkit2gtk-4.1 and gtk+-3.0 dev libraries (and pkg-config).\n' >&2
      printf 'Install the missing packages, then re-run this script.\n' >&2
      case "$PKG_MGR" in
        apt)    printf '  sudo apt-get install -y pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev\n' >&2 ;;
        dnf)    printf '  sudo dnf install -y pkgconf-pkg-config gtk3-devel webkit2gtk4.1-devel\n' >&2 ;;
        pacman) printf '  sudo pacman -S pkgconf gtk3 webkit2gtk-4.1\n' >&2 ;;
        *)      printf '  Install pkg-config, gtk3 dev, and webkit2gtk-4.1 dev via your package manager.\n' >&2 ;;
      esac
      exit 1
    fi
    printf '[ok]    webkit2gtk-4.1 and gtk+-3.0 dev libraries present\n'
  fi

  NODE_PINNED="$(tool_version nodejs)"
  if [ -z "$NODE_PINNED" ]; then
    die "Could not resolve nodejs version from .tool-versions — ensure the file has a 'nodejs <version>' line"
  fi
  NODE_PINNED_MAJOR="${NODE_PINNED%%.*}"
  _node_ok=0
  if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
    _node_ver="$(node --version 2>/dev/null | sed 's/^v//')"
    _node_major="${_node_ver%%.*}"
    case "$_node_major" in
      '' | *[!0-9]*) ;;
      *) [ "$_node_major" -ge "$NODE_PINNED_MAJOR" ] && _node_ok=1 ;;
    esac
  fi
  if [ "$_node_ok" = "0" ]; then
    printf 'error: building the perch frontend requires Node.js >= %s (pinned: %s) and npm.\n' \
      "$NODE_PINNED_MAJOR" "$NODE_PINNED" >&2
    printf 'Install it from https://nodejs.org/ or a version manager (nvm, asdf), then re-run this script.\n' >&2
    printf 'Use --skip-build to install only the agents.\n' >&2
    exit 1
  fi
  printf '[ok]    node %s and npm present\n' "$_node_ver"
fi

if [ "$UPGRADE" = "source" ]; then
  upgrade_source_checkout
fi

# ---------------------------------------------------------------------------
# Step 1: Go
# ---------------------------------------------------------------------------
GO_VERSION="$(tool_version golang)"
if [ -z "$GO_VERSION" ]; then
  die "Could not resolve golang version from .tool-versions — ensure the file exists and has a 'golang <version>' line"
fi

# go.mod carries a "toolchain go<GO_VERSION>" line. A local Go older than the
# pin would have to download that toolchain at build time (and fails under
# GOTOOLCHAIN=local or without network), so require at least the pinned version.
# Prefer a Go already on PATH (distro, brew, asdf); otherwise check the fixed
# install location used by this script.
go_meets_minimum() {
  # Returns 0 (true) if the given go binary is at least GO_VERSION.
  _go_bin="$1"
  if ! "$_go_bin" version >/dev/null 2>&1; then
    return 1
  fi
  _ver="$("$_go_bin" version | awk '{print $3}' | sed 's/^go//')"
  version_ge "$_ver" "$GO_VERSION"
}

GO_BIN=""
if command -v go >/dev/null 2>&1 && go_meets_minimum "$(command -v go)"; then
  GO_BIN="$(command -v go)"
elif [ -x /usr/local/go/bin/go ] && go_meets_minimum /usr/local/go/bin/go; then
  GO_BIN="/usr/local/go/bin/go"
fi

if [ -n "$GO_BIN" ]; then
  printf '[skip] go %s already installed at %s\n' \
    "$("$GO_BIN" version | awk '{print $3}' | sed 's/^go//')" "$GO_BIN"
else
  TARBALL="go${GO_VERSION}.${GOOS}-${ARCH}.tar.gz"
  DOWNLOAD_URL="https://go.dev/dl/${TARBALL}"

  printf '[install] go %s from %s\n' "$GO_VERSION" "$DOWNLOAD_URL"

  # Fetch the SHA256 value from go.dev JSON. Use grep and awk only, not jq.
  # The JSON lists filename, os, arch, version, and sha256 in that order.
  # The -A5 flag captures all five fields.
  EXPECTED_SHA="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' \
    || die "Failed to fetch Go release metadata from go.dev")"
  EXPECTED_SHA="$(printf '%s' "$EXPECTED_SHA" \
    | grep -A5 "\"${TARBALL}\"" \
    | grep '"sha256"' \
    | awk -F'"' '{print $4}')"

  if [ -z "$EXPECTED_SHA" ]; then
    die "Could not fetch SHA256 for ${TARBALL} from go.dev"
  fi

  # The template ends in the X's: BSD mktemp only substitutes trailing X's.
  TMPFILE="$(mktemp "${TMPDIR:-/tmp}/go-install.XXXXXX")"
  # Use a staging directory on the same filesystem as /usr/local, so the move is atomic.
  STAGE_DIR="/usr/local/.perch-go-$$"
  # on_exit removes the temp file and staging directory on any exit,
  # including on errors.
  GO_TMPFILE="$TMPFILE"
  GO_STAGE_DIR="$STAGE_DIR"

  curl -fsSL -o "$TMPFILE" "$DOWNLOAD_URL" \
    || die "Failed to download Go tarball from ${DOWNLOAD_URL}"

  # Verify checksum.
  if [ "$GOOS" = "linux" ]; then
    ACTUAL_SHA="$(sha256sum "$TMPFILE" | awk '{print $1}')"
  else
    ACTUAL_SHA="$(shasum -a 256 "$TMPFILE" | awk '{print $1}')"
  fi

  if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    die "SHA256 mismatch for ${TARBALL}: got ${ACTUAL_SHA}, expected ${EXPECTED_SHA}"
  fi

  # Extract atomically: extract into a staging directory, verify it, then swap it in.
  $SUDO mkdir -p "$STAGE_DIR"
  $SUDO tar -C "$STAGE_DIR" -xzf "$TMPFILE"

  # Verify the expected binary exists in the staged tree before committing the swap.
  if [ ! -x "${STAGE_DIR}/go/bin/go" ]; then
    die "Extraction of ${TARBALL} did not produce go/bin/go — aborting (no change made to /usr/local/go)"
  fi

  # Swap in the new install. Remove the old installation, then move the staged tree into place.
  $SUDO rm -rf /usr/local/go
  $SUDO mv "${STAGE_DIR}/go" /usr/local/go

  # Clean up temp files now, rather than on exit.
  rm -f "$TMPFILE"
  $SUDO rm -rf "$STAGE_DIR"
  GO_TMPFILE=""
  GO_STAGE_DIR=""

  printf '[ok]    go %s installed at /usr/local/go\n' "$GO_VERSION"
fi

# Ensure Go is on PATH for the remainder of this script.
if [ -n "$GO_BIN" ]; then
  PATH="$(dirname "$GO_BIN"):$PATH"
else
  PATH="/usr/local/go/bin:$PATH"
fi
export PATH

# ---------------------------------------------------------------------------
# Step 2: git (required; the script exits with an error if git is absent or too old)
# ---------------------------------------------------------------------------
GIT_MIN_MAJOR=2
GIT_MIN_MINOR=20

git_meets_minimum() {
  if ! command -v git >/dev/null 2>&1; then
    return 1
  fi
  _ver="$(git --version | awk '{print $3}')"
  _major="$(printf '%s' "$_ver" | cut -d. -f1)"
  # Strip trailing letters before numeric comparison.
  _minor="$(printf '%s' "$_ver" | cut -d. -f2 | sed 's/[^0-9].*//')"
  [ "$_major" -gt "$GIT_MIN_MAJOR" ] || \
    { [ "$_major" -eq "$GIT_MIN_MAJOR" ] && [ "$_minor" -ge "$GIT_MIN_MINOR" ]; }
}

if git_meets_minimum; then
  printf '[skip] git %s already installed\n' "$(git --version | awk '{print $3}')"
else
  printf 'error: git >= %d.%d is required but not found.\n' \
    "$GIT_MIN_MAJOR" "$GIT_MIN_MINOR" >&2
  printf 'Install instructions:\n' >&2
  case "$OS" in
    Linux)
      case "$PKG_MGR" in
        apt)    printf '  sudo apt-get install -y git\n' >&2 ;;
        dnf)    printf '  sudo dnf install -y git\n' >&2 ;;
        pacman) printf '  sudo pacman -S git\n' >&2 ;;
        *)      printf '  Install git from your distribution package manager.\n' >&2 ;;
      esac
      ;;
    Darwin)
      printf '  brew install git  (or install Xcode Command Line Tools)\n' >&2 ;;
  esac
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 3: claude
# ---------------------------------------------------------------------------
if [ "$SKIP_AGENTS" = "1" ]; then
  printf '[skip] claude (--skip-agents)\n'
else
  CLAUDE_VERSION="$(tool_version claude)"
  if [ -z "$CLAUDE_VERSION" ]; then
    die "Could not resolve claude version from .tool-versions — ensure the file has a 'claude <version>' line"
  fi
  if command -v claude >/dev/null 2>&1; then
    INSTALLED_CLAUDE="$(installed_version claude || true)"
    if [ "$INSTALLED_CLAUDE" = "$CLAUDE_VERSION" ]; then
      printf '[skip] claude %s already installed\n' "$INSTALLED_CLAUDE"
    else
      printf '[warn]  claude %s installed; .tool-versions pins %s\n' \
        "$INSTALLED_CLAUDE" "$CLAUDE_VERSION"
      printf '        Run '"'"'claude update'"'"' or edit .tool-versions to match.\n'
    fi
  else
    printf '[install] claude via https://cli.anthropic.com/install.sh\n'
    run_remote_installer claude https://cli.anthropic.com/install.sh || true
    INSTALLED_CLAUDE="$(installed_version claude || true)"
    if [ -z "$INSTALLED_CLAUDE" ]; then
      printf '[warn]  claude is not installed; continuing without it\n'
    elif [ "$INSTALLED_CLAUDE" = "$CLAUDE_VERSION" ]; then
      printf '[ok]    claude %s installed\n' "$INSTALLED_CLAUDE"
    else
      printf '[warn]  claude %s installed; .tool-versions pins %s\n' \
        "$INSTALLED_CLAUDE" "$CLAUDE_VERSION"
      printf '        Run '"'"'claude update'"'"' or edit .tool-versions to match.\n'
    fi
  fi
fi

# ---------------------------------------------------------------------------
# Step 4: opencode
# ---------------------------------------------------------------------------
if [ "$SKIP_AGENTS" = "1" ]; then
  printf '[skip] opencode (--skip-agents)\n'
else
  OPENCODE_VERSION="$(tool_version opencode)"
  if [ -z "$OPENCODE_VERSION" ]; then
    die "Could not resolve opencode version from .tool-versions — ensure the file has an 'opencode <version>' line"
  fi
  if command -v opencode >/dev/null 2>&1; then
    INSTALLED_OPENCODE="$(installed_version opencode || true)"
    if [ "$INSTALLED_OPENCODE" = "$OPENCODE_VERSION" ]; then
      printf '[skip] opencode %s already installed\n' "$INSTALLED_OPENCODE"
    else
      printf '[warn]  opencode %s installed; .tool-versions pins %s\n' \
        "$INSTALLED_OPENCODE" "$OPENCODE_VERSION"
      printf '        Run '"'"'opencode update'"'"' or edit .tool-versions to match.\n'
    fi
  else
    printf '[install] opencode via https://opencode.ai/install\n'
    run_remote_installer opencode https://opencode.ai/install || true
    INSTALLED_OPENCODE="$(installed_version opencode || true)"
    if [ -z "$INSTALLED_OPENCODE" ]; then
      printf '[warn]  opencode is not installed; continuing without it\n'
    elif [ "$INSTALLED_OPENCODE" = "$OPENCODE_VERSION" ]; then
      printf '[ok]    opencode %s installed\n' "$INSTALLED_OPENCODE"
    else
      printf '[warn]  opencode %s installed; .tool-versions pins %s\n' \
        "$INSTALLED_OPENCODE" "$OPENCODE_VERSION"
      printf '        Run '"'"'opencode update'"'"' or edit .tool-versions to match.\n'
    fi
  fi
fi

# ---------------------------------------------------------------------------
# Resolve INSTALL_PREFIX once. The build step installs the perch binary here.
# ---------------------------------------------------------------------------
if [ -z "$INSTALL_PREFIX" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_PREFIX="/usr/local/bin"
  else
    INSTALL_PREFIX="${HOME}/.local/bin"
  fi
fi
mkdir -p "$INSTALL_PREFIX" || die "Cannot create ${INSTALL_PREFIX}"

# ---------------------------------------------------------------------------
# Step 6: build perch
# A normal install builds straight into the prefix. --upgrade=source builds
# into a temp file next to it, smoke-tests it, then swaps it in.
# ---------------------------------------------------------------------------
if [ "$SKIP_BUILD" = "1" ]; then
  printf '[skip] perch build (--skip-build)\n'
else
  BUILD_OUT="${INSTALL_PREFIX}/perch"
  if [ "$UPGRADE" = "source" ]; then
    make_upgrade_tmp
    BUILD_OUT="${UPGRADE_TMP}/perch"
  fi
  printf '[install] building perch -> %s/perch\n' "$INSTALL_PREFIX"
  cd "$REPO_ROOT" || die "Cannot cd to repo root: ${REPO_ROOT}"
  if [ "$UPGRADE" = "source" ]; then
    # Stamp the tag itself. git describe could name another tag on the same
    # commit (say an annotated v1.0.0-rc1 next to a lightweight v1.0.0), and
    # the smoke test requires exactly NEW_TAG.
    PERCH_VERSION="$NEW_TAG"
  else
    PERCH_VERSION="$(git describe --tags --always 2>/dev/null || printf 'dev')"
  fi

  # The committed frontend/dist/index.html is only a go:embed stub, and app.Run
  # refuses to start with it. Build the real frontend first, as `make
  # gui-build` does. on_exit restores the stub if the build fails.
  printf '[install] building frontend (npm ci && npm run build)\n'
  DIST_TOUCHED=1
  npm --prefix frontend ci || die "npm ci failed in frontend/"
  npm --prefix frontend run build || die "npm run build failed in frontend/"
  if [ ! -s frontend/dist/index.html ] || ! grep -q '<script' frontend/dist/index.html; then
    die "frontend build did not produce a real frontend/dist/index.html"
  fi

  go build -tags "production webkit2_41" -trimpath \
    -ldflags "-s -w -X main.version=${PERCH_VERSION}" \
    -o "$BUILD_OUT" ./cmd/perch

  # vite overwrote the tracked stub. The binary already embeds the real index,
  # so restore the stub to leave the work tree clean.
  git -C "$REPO_ROOT" checkout -- frontend/dist/index.html 2>/dev/null || true
  DIST_TOUCHED=0

  if [ "$UPGRADE" = "source" ]; then
    smoke_test "$BUILD_OUT"
    swap_in "$BUILD_OUT"
    # The new binary is in place: keep the checkout on the tag it came from.
    if [ -n "${RESTORE_REF:-}" ]; then
      printf '[upgrade] the checkout stays at %s; "git checkout %s" returns to where it was\n' \
        "$NEW_TAG" "$RESTORE_REF"
      RESTORE_REF=""
    fi
  else
    "$BUILD_OUT" version >/dev/null 2>&1 \
      || die "the built binary at ${BUILD_OUT} does not run"
  fi
  printf '[ok]    perch built at %s/perch\n' "$INSTALL_PREFIX"
fi

# ---------------------------------------------------------------------------
# Step 7: desktop integration (Linux): icon and .desktop entry
# The binary already carries the window icon, embedded through
# options.Linux.Icon, but GNOME on Wayland shows the dock and switcher icon
# only through a perch.desktop whose StartupWMClass (perch) matches the
# window. A perch with the install-desktop subcommand writes it; otherwise
# the inline fallback in write_desktop_entry_inline does.
# ---------------------------------------------------------------------------
desktop_entry "${INSTALL_PREFIX}/perch"

if [ "$UPGRADE" = "source" ]; then
  finish_upgrade
fi
