#!/bin/sh
# install.sh: sets up perch and its runtime dependencies.
# This script uses POSIX sh only, with no bash-specific syntax. It is
# shellcheck-clean for the sh dialect.
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

# Remember where the user ran the script, so a relative --prefix resolves
# against that directory and not against the repo root.
START_DIR="$(pwd)"

for arg in "$@"; do
  case "$arg" in
    --skip-agents) SKIP_AGENTS=1 ;;
    --skip-build)  SKIP_BUILD=1  ;;
    --prefix=*)    INSTALL_PREFIX="${arg#--prefix=}" ;;
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
    _runner=sh
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

# Determine whether to use sudo (never call sudo when already root).
if [ "$(id -u)" = "0" ]; then
  SUDO=""
else
  SUDO="sudo"
fi

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
  # Remove the temp file and staging directory on any exit, including on errors.
  trap '$SUDO rm -rf "$TMPFILE" "$STAGE_DIR"' EXIT

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

  # Clean up temp files now. The trap also fires on EXIT, but this cleans up sooner.
  rm -f "$TMPFILE"
  $SUDO rm -rf "$STAGE_DIR"
  trap '' EXIT

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
# ---------------------------------------------------------------------------
if [ "$SKIP_BUILD" = "1" ]; then
  printf '[skip] perch build (--skip-build)\n'
else
  printf '[install] building perch -> %s/perch\n' "$INSTALL_PREFIX"
  cd "$REPO_ROOT" || die "Cannot cd to repo root: ${REPO_ROOT}"
  PERCH_VERSION="$(git describe --tags --always 2>/dev/null || printf 'dev')"

  # The committed frontend/dist/index.html is only a go:embed stub, and app.Run
  # refuses to start with it. Build the real frontend first, as `make
  # gui-build` does.
  printf '[install] building frontend (npm ci && npm run build)\n'
  npm --prefix frontend ci || die "npm ci failed in frontend/"
  npm --prefix frontend run build || die "npm run build failed in frontend/"
  if [ ! -s frontend/dist/index.html ] || ! grep -q '<script' frontend/dist/index.html; then
    die "frontend build did not produce a real frontend/dist/index.html"
  fi

  go build -tags "production webkit2_41" -trimpath \
    -ldflags "-s -w -X main.version=${PERCH_VERSION}" \
    -o "${INSTALL_PREFIX}/perch" ./cmd/perch

  # vite overwrote the tracked stub. The binary already embeds the real index,
  # so restore the stub to leave the work tree clean.
  git -C "$REPO_ROOT" checkout -- frontend/dist/index.html 2>/dev/null || true

  "${INSTALL_PREFIX}/perch" version >/dev/null 2>&1 \
    || die "the built binary at ${INSTALL_PREFIX}/perch does not run"
  printf '[ok]    perch built at %s/perch\n' "$INSTALL_PREFIX"
fi

# ---------------------------------------------------------------------------
# Step 7: desktop integration (Linux): icon and .desktop entry
# The binary already carries the window icon, embedded through
# options.Linux.Icon. This step adds the app-menu and app-switcher entry.
# Its StartupWMClass matches ProgramName, so the switcher shows the same icon.
# ---------------------------------------------------------------------------
if [ "$GOOS" = "linux" ] && [ ! -x "${INSTALL_PREFIX}/perch" ]; then
  printf '[skip] desktop entry (no perch binary at %s/perch)\n' "$INSTALL_PREFIX"
elif [ "$GOOS" = "linux" ]; then
  # Quote the Exec path (it may contain spaces) and double any % as the
  # Desktop Entry spec requires.
  exec_path="$(printf '%s' "${INSTALL_PREFIX}/perch" | sed 's/%/%%/g')"
  icon_dir="${HOME}/.local/share/icons/hicolor/512x512/apps"
  apps_dir="${HOME}/.local/share/applications"
  mkdir -p "$icon_dir" "$apps_dir"
  cp "${REPO_ROOT}/app/appicon.png" "${icon_dir}/perch.png"
  cat > "${apps_dir}/perch.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=perch
Comment=Cockpit for AI coding agents
Exec="${exec_path}"
Icon=perch
Terminal=false
Categories=Development;
StartupWMClass=perch
DESKTOP
  printf '[ok]    desktop entry installed at %s/perch.desktop\n' "$apps_dir"
fi
