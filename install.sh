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

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
die() { printf 'error: %s\n' "$1" >&2; exit 1; }

# Read a pinned version from .tool-versions.
tool_version() {
  grep "^$1 " "${REPO_ROOT}/.tool-versions" | awk '{print $2}'
}

# Return the version reported by a CLI tool (last whitespace-delimited field).
installed_version() {
  "$1" --version 2>/dev/null | awk '{print $NF}'
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
# Step 1: Go
# ---------------------------------------------------------------------------
GO_VERSION="$(tool_version golang)"
if [ -z "$GO_VERSION" ]; then
  die "Could not resolve golang version from .tool-versions — ensure the file exists and has a 'golang <version>' line"
fi
GO_MIN_MAJOR=1
GO_MIN_MINOR=24

# Check the fixed path, not PATH, so re-running this script gives the same result.
GO_BIN="/usr/local/go/bin/go"

go_meets_minimum() {
  # Returns 0 (true) if the given go binary meets the minimum version.
  _go_bin="$1"
  if ! "$_go_bin" version >/dev/null 2>&1; then
    return 1
  fi
  _ver="$("$_go_bin" version | awk '{print $3}' | sed 's/go//')"
  _major="$(printf '%s' "$_ver" | cut -d. -f1)"
  # Strip trailing letters before numeric comparison.
  _minor="$(printf '%s' "$_ver" | cut -d. -f2 | sed 's/[^0-9].*//')"
  [ "$_major" -gt "$GO_MIN_MAJOR" ] || \
    { [ "$_major" -eq "$GO_MIN_MAJOR" ] && [ "$_minor" -ge "$GO_MIN_MINOR" ]; }
}

if [ -x "$GO_BIN" ] && go_meets_minimum "$GO_BIN"; then
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

  TMPFILE="$(mktemp /tmp/go-install-XXXXXX.tar.gz)"
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
export PATH="/usr/local/go/bin:$PATH"

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
    curl -fsSL https://cli.anthropic.com/install.sh | sh
    INSTALLED_CLAUDE="$(installed_version claude || true)"
    if [ "$INSTALLED_CLAUDE" = "$CLAUDE_VERSION" ]; then
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
    curl -fsSL https://opencode.ai/install | sh
    INSTALLED_OPENCODE="$(installed_version opencode || true)"
    if [ "$INSTALLED_OPENCODE" = "$OPENCODE_VERSION" ]; then
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
    mkdir -p "$INSTALL_PREFIX"
  fi
fi

# ---------------------------------------------------------------------------
# Step 5: webkit2gtk-4.1 + gtk+-3.0 dev libraries (Linux GUI build deps)
# ---------------------------------------------------------------------------
if [ "$OS" = "Linux" ] && [ "$SKIP_BUILD" != "1" ]; then
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

# ---------------------------------------------------------------------------
# Step 6: build perch
# ---------------------------------------------------------------------------
if [ "$SKIP_BUILD" = "1" ]; then
  printf '[skip] perch build (--skip-build)\n'
else
  printf '[install] building perch -> %s/perch\n' "$INSTALL_PREFIX"
  cd "$REPO_ROOT" || die "Cannot cd to repo root: ${REPO_ROOT}"
  go build -tags "production webkit2_41" -trimpath \
    -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || printf 'dev')" \
    -o "${INSTALL_PREFIX}/perch" ./cmd/perch
  printf '[ok]    perch built at %s/perch\n' "$INSTALL_PREFIX"
fi

# ---------------------------------------------------------------------------
# Step 7: desktop integration (Linux): icon and .desktop entry
# The binary already carries the window icon, embedded through
# options.Linux.Icon. This step adds the app-menu and app-switcher entry.
# Its StartupWMClass matches ProgramName, so the switcher shows the same icon.
# ---------------------------------------------------------------------------
if [ "$GOOS" = "linux" ]; then
  icon_dir="${HOME}/.local/share/icons/hicolor/512x512/apps"
  apps_dir="${HOME}/.local/share/applications"
  mkdir -p "$icon_dir" "$apps_dir"
  cp "${REPO_ROOT}/app/appicon.png" "${icon_dir}/perch.png"
  cat > "${apps_dir}/perch.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=perch
Comment=Cockpit for AI coding agents
Exec=${INSTALL_PREFIX}/perch
Icon=perch
Terminal=false
Categories=Development;
StartupWMClass=perch
DESKTOP
  printf '[ok]    desktop entry installed at %s/perch.desktop\n' "$apps_dir"
fi
