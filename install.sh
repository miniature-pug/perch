#!/bin/sh
# install.sh — bootstrap perch and its runtime dependencies.
# POSIX sh; no bashisms. shellcheck-clean (dialect: sh).
# See plan.md §21.3 for the full specification.
set -e

# ---------------------------------------------------------------------------
# Repo root — derived from script location, not cwd.
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$SCRIPT_DIR"

# ---------------------------------------------------------------------------
# Flags
# ---------------------------------------------------------------------------
SKIP_AGENTS=0
SKIP_BUILD=0
SKIP_SETUP=0
INSTALL_PREFIX=""
YES=0
export YES  # used by sub-scripts invoked from this one (e.g. perch setup)

for arg in "$@"; do
  case "$arg" in
    --skip-agents) SKIP_AGENTS=1 ;;
    --skip-build)  SKIP_BUILD=1  ;;
    --skip-setup)  SKIP_SETUP=1  ;;
    --prefix=*)    INSTALL_PREFIX="${arg#--prefix=}" ;;
    --yes)         YES=1         ;;
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

pkg_install() {
  case "$PKG_MGR" in
    apt)    $SUDO apt-get install -y "$1" ;;
    dnf)    $SUDO dnf install -y "$1" ;;
    brew)   brew install "$1" ;;
    pacman) $SUDO pacman -S --noconfirm "$1" ;;
    *) die "No supported package manager found. Please install $1 manually." ;;
  esac
}

# ---------------------------------------------------------------------------
# Ensure curl is available (not present by default on Ubuntu base image)
# ---------------------------------------------------------------------------
if ! command -v curl >/dev/null 2>&1; then
  printf '[install] curl (required for downloads)\n'
  pkg_install curl
fi

# ---------------------------------------------------------------------------
# Step 1: Go
# ---------------------------------------------------------------------------
GO_VERSION="$(tool_version golang)"
GO_MIN_MAJOR=1
GO_MIN_MINOR=24

# Check by path, not PATH, so idempotency survives across script invocations.
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

  # Fetch SHA256 from go.dev JSON — grep/awk only, no jq.
  # The JSON lists filename, os, arch, version, sha256 in that order; -A5 covers it.
  EXPECTED_SHA="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' \
    | grep -A5 "\"${TARBALL}\"" \
    | grep '"sha256"' \
    | awk -F'"' '{print $4}')"

  if [ -z "$EXPECTED_SHA" ]; then
    die "Could not fetch SHA256 for ${TARBALL} from go.dev"
  fi

  TMPFILE="$(mktemp /tmp/go-install-XXXXXX.tar.gz)"
  # Remove tmpfile on exit to avoid leaking even on error.
  trap 'rm -f "$TMPFILE"' EXIT

  curl -fsSL -o "$TMPFILE" "$DOWNLOAD_URL"

  # Verify checksum.
  if [ "$GOOS" = "linux" ]; then
    ACTUAL_SHA="$(sha256sum "$TMPFILE" | awk '{print $1}')"
  else
    ACTUAL_SHA="$(shasum -a 256 "$TMPFILE" | awk '{print $1}')"
  fi

  if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    die "SHA256 mismatch for ${TARBALL}: got ${ACTUAL_SHA}, expected ${EXPECTED_SHA}"
  fi

  # Extract — remove any previous installation first.
  $SUDO rm -rf /usr/local/go
  $SUDO tar -C /usr/local -xzf "$TMPFILE"
  rm -f "$TMPFILE"
  trap '' EXIT

  printf '[ok]    go %s installed at /usr/local/go\n' "$GO_VERSION"
fi

# Ensure Go is on PATH for the remainder of this script.
export PATH="/usr/local/go/bin:$PATH"

# ---------------------------------------------------------------------------
# Step 2: tmux
# ---------------------------------------------------------------------------
TMUX_MIN_MAJOR=3
TMUX_MIN_MINOR=2

tmux_meets_minimum() {
  if ! command -v tmux >/dev/null 2>&1; then
    return 1
  fi
  _ver="$(tmux -V | awk '{print $2}')"
  _major="$(printf '%s' "$_ver" | cut -d. -f1)"
  # Strip trailing letters (e.g. "5a" -> "5") before numeric comparison.
  _minor="$(printf '%s' "$_ver" | cut -d. -f2 | sed 's/[^0-9].*//')"
  [ "$_major" -gt "$TMUX_MIN_MAJOR" ] || \
    { [ "$_major" -eq "$TMUX_MIN_MAJOR" ] && [ "$_minor" -ge "$TMUX_MIN_MINOR" ]; }
}

if tmux_meets_minimum; then
  printf '[skip] tmux %s already installed\n' "$(tmux -V | awk '{print $2}')"
else
  if command -v tmux >/dev/null 2>&1; then
    printf '[install] tmux (upgrading from %s)\n' "$(tmux -V | awk '{print $2}')"
  else
    printf '[install] tmux\n'
  fi

  if [ -n "$PKG_MGR" ]; then
    pkg_install tmux
    if tmux_meets_minimum; then
      printf '[ok]    tmux %s installed\n' "$(tmux -V | awk '{print $2}')"
    else
      printf '[warn]  tmux installed but version is below minimum %d.%d — check your package manager\n' \
        "$TMUX_MIN_MAJOR" "$TMUX_MIN_MINOR"
    fi
  else
    printf '[warn]  no package manager found; please install tmux >= %d.%d manually\n' \
      "$TMUX_MIN_MAJOR" "$TMUX_MIN_MINOR"
  fi
fi

# Pin-drift check: warn if installed tmux is at/above minimum but below the
# pinned version in .tool-versions (§21.1/§21.2 single-source-of-truth).
# Read the pin — never hardcode a version here.
TMUX_PINNED="$(tool_version tmux)"
if command -v tmux >/dev/null 2>&1 && [ -n "$TMUX_PINNED" ]; then
  _inst_ver="$(tmux -V | awk '{print $2}')"
  _inst_major="$(printf '%s' "$_inst_ver" | cut -d. -f1)"
  _inst_minor="$(printf '%s' "$_inst_ver" | cut -d. -f2 | sed 's/[^0-9].*//')"
  _pin_major="$(printf '%s' "$TMUX_PINNED" | cut -d. -f1)"
  _pin_minor="$(printf '%s' "$TMUX_PINNED" | cut -d. -f2 | sed 's/[^0-9].*//')"
  # Only warn when installed is below the pin (and at/above min, so still usable).
  _below_pin=0
  if [ "$_inst_major" -lt "$_pin_major" ]; then
    _below_pin=1
  elif [ "$_inst_major" -eq "$_pin_major" ] && [ "$_inst_minor" -lt "$_pin_minor" ]; then
    _below_pin=1
  fi
  if [ "$_below_pin" = "1" ] && tmux_meets_minimum; then
    printf '[warn]  tmux %s installed; .tool-versions pins %s\n' \
      "$_inst_ver" "$TMUX_PINNED"
    printf '        Run '"'"'tmux update'"'"' or upgrade via your package manager to match.\n'
  fi
fi

# ---------------------------------------------------------------------------
# Step 3: git (non-fatal install check; exit 1 if absent or too old)
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
# Step 4: claude
# ---------------------------------------------------------------------------
if [ "$SKIP_AGENTS" = "1" ]; then
  printf '[skip] claude (--skip-agents)\n'
else
  CLAUDE_VERSION="$(tool_version claude)"
  if command -v claude >/dev/null 2>&1; then
    INSTALLED_CLAUDE="$(claude --version 2>/dev/null | awk '{print $NF}' || true)"
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
    INSTALLED_CLAUDE="$(claude --version 2>/dev/null | awk '{print $NF}' || true)"
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
# Step 5: opencode
# ---------------------------------------------------------------------------
if [ "$SKIP_AGENTS" = "1" ]; then
  printf '[skip] opencode (--skip-agents)\n'
else
  OPENCODE_VERSION="$(tool_version opencode)"
  if command -v opencode >/dev/null 2>&1; then
    INSTALLED_OPENCODE="$(opencode --version 2>/dev/null | awk '{print $NF}' || true)"
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
    INSTALLED_OPENCODE="$(opencode --version 2>/dev/null | awk '{print $NF}' || true)"
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
# Resolve INSTALL_PREFIX once — used by both step 6 (build) and step 7 (setup).
# Must be set before either step so that --skip-build + run-setup still works.
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
# Step 6: build perch
# ---------------------------------------------------------------------------
if [ "$SKIP_BUILD" = "1" ]; then
  printf '[skip] perch build (--skip-build)\n'
else
  printf '[install] building perch -> %s/perch\n' "$INSTALL_PREFIX"
  cd "$REPO_ROOT"
  go build -trimpath \
    -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || printf 'dev')" \
    -o "${INSTALL_PREFIX}/perch" ./cmd/perch
  printf '[ok]    perch built at %s/perch\n' "$INSTALL_PREFIX"
fi

# ---------------------------------------------------------------------------
# Step 7: perch setup
# ---------------------------------------------------------------------------
if [ "$SKIP_SETUP" = "1" ]; then
  printf '[skip] perch setup (--skip-setup)\n'
else
  printf '[install] running perch setup\n'
  "${INSTALL_PREFIX}/perch" setup
  printf '[ok]    perch setup complete\n'
fi
