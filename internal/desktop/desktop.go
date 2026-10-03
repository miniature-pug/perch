// Package desktop installs perch's freedesktop.org integration for the
// current user: the app icon in the hicolor theme and a perch.desktop entry.
//
// GNOME on Wayland ignores the icon a window sets for itself. The dock and
// the app switcher take the icon only from a .desktop file that matches the
// window (Wayland app_id "perch", X11 WM_CLASS "perch") and whose Icon= key
// resolves in the icon theme. Without that entry the shell shows the generic
// application icon. Install writes both files under $XDG_DATA_HOME (default
// ~/.local/share), so no root access is needed.
//
// The package has no GUI dependency. The caller passes the icon bytes (the
// PNG app/ embeds) and the absolute path of the perch binary.
package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/miniature-pug/perch/internal/proc"
)

// AppID is the desktop file ID (without .desktop), the icon name, and the
// StartupWMClass. It matches the Wayland app_id and the X11 WM_CLASS of the
// perch window.
const AppID = "perch"

// Comment is the entry's Comment= value. It matches the Makefile desktop
// target.
const Comment = "Cockpit for AI coding agents"

// IconSize is the pixel size of the embedded icon (app/appicon.png is
// 512x512). It selects the hicolor size directory.
const IconSize = 512

// DefaultRefreshTimeout bounds each cache-refresh command.
const DefaultRefreshTimeout = 10 * time.Second

// Paths are the locations Install writes to.
type Paths struct {
	// DataHome is the XDG data directory, $XDG_DATA_HOME or ~/.local/share.
	DataHome string
	// HicolorDir is DataHome/icons/hicolor, the directory whose icon cache
	// is refreshed.
	HicolorDir string
	// IconFile is HicolorDir/512x512/apps/perch.png.
	IconFile string
	// ApplicationsDir is DataHome/applications.
	ApplicationsDir string
	// DesktopFile is ApplicationsDir/perch.desktop.
	DesktopFile string
}

// PathsFor returns the install locations under dataHome.
func PathsFor(dataHome string) Paths {
	hicolor := filepath.Join(dataHome, "icons", "hicolor")
	apps := filepath.Join(dataHome, "applications")
	size := fmt.Sprintf("%dx%d", IconSize, IconSize)
	return Paths{
		DataHome:        dataHome,
		HicolorDir:      hicolor,
		IconFile:        filepath.Join(hicolor, size, "apps", AppID+".png"),
		ApplicationsDir: apps,
		DesktopFile:     filepath.Join(apps, AppID+".desktop"),
	}
}

// DataHome resolves the XDG data directory: $XDG_DATA_HOME when it is set to
// an absolute path (the spec says to ignore a relative one), otherwise
// $HOME/.local/share.
func DataHome() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Clean(d), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("desktop: cannot resolve the data directory: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

// DefaultPaths returns PathsFor(DataHome()).
func DefaultPaths() (Paths, error) {
	dh, err := DataHome()
	if err != nil {
		return Paths{}, err
	}
	return PathsFor(dh), nil
}

// Options configures Install.
type Options struct {
	// BinPath is the absolute path of the perch binary the entry launches.
	// Required.
	BinPath string
	// Icon is the PNG written as the hicolor icon. Required.
	Icon []byte
	// DataHome overrides the XDG data directory. Empty means DataHome().
	DataHome string
	// Runner runs the cache-refresh commands. Nil means proc.ExecRunner{}.
	Runner proc.Runner
	// LookPath finds the cache-refresh commands. Nil means exec.LookPath. A
	// command LookPath cannot find is skipped.
	LookPath func(string) (string, error)
	// RefreshTimeout bounds each refresh command. Zero means
	// DefaultRefreshTimeout.
	RefreshTimeout time.Duration
}

// Install writes the icon and the perch.desktop entry, each atomically
// (temp file plus rename), then refreshes the icon cache and the desktop
// database on a best-effort basis. A refresh failure, a missing refresh tool,
// or a refresh timeout never fails Install: GNOME also picks the files up
// on its own, at the latest on the next login.
func Install(opts Options) error {
	entry, err := Entry(opts.BinPath)
	if err != nil {
		return err
	}
	if len(opts.Icon) == 0 {
		return errors.New("desktop: no icon data")
	}
	p, err := resolvePaths(opts.DataHome)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(p.IconFile, opts.Icon); err != nil {
		return err
	}
	if err := writeFileAtomic(p.DesktopFile, []byte(entry)); err != nil {
		return err
	}
	refreshCaches(opts, p)
	return nil
}

// State describes the installed entry relative to a given binary.
type State int

const (
	// Missing: the desktop file or the icon does not exist.
	Missing State = iota
	// Current: both files exist and Exec launches the given binary.
	Current
	// Dangling: both files exist, but Exec names a file that no longer
	// exists or is not executable (the binary moved or was deleted).
	Dangling
	// Other: both files exist and Exec launches a different, working
	// binary (for example the installed perch, while a dev build runs).
	Other
)

func (s State) String() string {
	switch s {
	case Missing:
		return "missing"
	case Current:
		return "current"
	case Dangling:
		return "dangling"
	case Other:
		return "other"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Status reports the state of the entry under dataHome (empty means
// DataHome()) relative to binPath. Exec is compared after filepath.Clean.
func Status(dataHome, binPath string) (State, error) {
	p, err := resolvePaths(dataHome)
	if err != nil {
		return Missing, err
	}
	data, err := os.ReadFile(p.DesktopFile)
	if errors.Is(err, os.ErrNotExist) {
		return Missing, nil
	}
	if err != nil {
		return Missing, fmt.Errorf("desktop: read %s: %w", p.DesktopFile, err)
	}
	if fi, err := os.Stat(p.IconFile); err != nil || fi.Size() == 0 {
		return Missing, nil
	}
	exe, ok := ExecProgram(string(data))
	if !ok {
		return Dangling, nil
	}
	if filepath.Clean(exe) == filepath.Clean(binPath) {
		return Current, nil
	}
	if !filepath.IsAbs(exe) {
		// A bare program name resolves through PATH at launch time.
		if _, err := exec.LookPath(exe); err != nil {
			return Dangling, nil
		}
		return Other, nil
	}
	if fi, err := os.Stat(exe); err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return Dangling, nil
	}
	return Other, nil
}

// IsInstalled reports whether both the perch.desktop entry and the icon
// exist under the default data directory.
func IsInstalled() bool {
	p, err := DefaultPaths()
	if err != nil {
		return false
	}
	return regularFile(p.DesktopFile) && regularFile(p.IconFile)
}

// NeedsUpdate reports whether a GUI started from binPath should
// (re)install the entry: when it is missing, or when its Exec target no
// longer runs. An entry that launches another working perch binary is left
// alone, so a dev build started from a checkout does not steal the entry
// from the installed binary (and the two do not flip-flop). The explicit
// `perch install-desktop` command always rewrites the entry.
func NeedsUpdate(binPath string) bool {
	st, err := Status("", binPath)
	if err != nil {
		return false
	}
	return st == Missing || st == Dangling
}

// Entry renders the perch.desktop contents for binPath. binPath must be an
// absolute path in valid UTF-8 with no control characters.
func Entry(binPath string) (string, error) {
	execVal, err := ExecValue(binPath)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=" + AppID + "\n")
	b.WriteString("Comment=" + Comment + "\n")
	b.WriteString("Exec=" + execVal + "\n")
	b.WriteString("Icon=" + AppID + "\n")
	b.WriteString("Terminal=false\n")
	b.WriteString("Categories=Development;\n")
	b.WriteString("StartupWMClass=" + AppID + "\n")
	return b.String(), nil
}

// ExecValue encodes binPath as the value of an Exec= key, per the Desktop
// Entry Specification: the path is one double-quoted argument with ", `, $
// and \ backslash-escaped (the quoting rule), a literal % doubled to %% (the
// field-code rule), and every backslash then doubled again (the string
// escape rule, which applies before quoting). A literal backslash so ends up
// as four backslashes.
func ExecValue(binPath string) (string, error) {
	if !filepath.IsAbs(binPath) {
		return "", fmt.Errorf("desktop: binary path %q is not absolute", binPath)
	}
	if !utf8.ValidString(binPath) {
		return "", fmt.Errorf("desktop: binary path %q is not valid UTF-8", binPath)
	}
	for _, r := range binPath {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("desktop: binary path %q contains a control character", binPath)
		}
	}
	var q strings.Builder
	q.WriteByte('"')
	for _, r := range binPath {
		switch r {
		case '"', '`', '$', '\\':
			q.WriteByte('\\')
			q.WriteRune(r)
		case '%':
			q.WriteString("%%")
		default:
			q.WriteRune(r)
		}
	}
	q.WriteByte('"')
	return strings.ReplaceAll(q.String(), `\`, `\\`), nil
}

// ExecProgram extracts the program (first argument) of the Exec= key in the
// [Desktop Entry] group of contents, reversing the encoding ExecValue
// applies. It also reads an unquoted Exec (as the Makefile writes it). ok is
// false when there is no Exec key or it cannot be parsed.
func ExecProgram(contents string) (prog string, ok bool) {
	raw, found := execKey(contents)
	if !found {
		return "", false
	}
	val := unescapeString(raw)
	val = strings.TrimLeft(val, " ")
	if val == "" {
		return "", false
	}
	var arg strings.Builder
	if val[0] == '"' {
		closed := false
		for i := 1; i < len(val); i++ {
			c := val[i]
			if c == '\\' && i+1 < len(val) {
				i++
				arg.WriteByte(val[i])
				continue
			}
			if c == '"' {
				closed = true
				break
			}
			arg.WriteByte(c)
		}
		if !closed {
			return "", false
		}
	} else {
		end := strings.IndexByte(val, ' ')
		if end < 0 {
			end = len(val)
		}
		arg.WriteString(val[:end])
	}
	prog = strings.ReplaceAll(arg.String(), "%%", "%")
	return prog, prog != ""
}

// execKey returns the raw Exec value in the [Desktop Entry] group.
func execKey(contents string) (string, bool) {
	inMain := false
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inMain = trimmed == "[Desktop Entry]"
			continue
		}
		if !inMain || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != "Exec" {
			continue
		}
		return strings.TrimSpace(val), true
	}
	return "", false
}

// unescapeString applies the string-type escape rules (\s \n \t \r \\).
// An unknown escape is kept as is, so the quoting layer still sees it.
func unescapeString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func resolvePaths(dataHome string) (Paths, error) {
	if dataHome == "" {
		return DefaultPaths()
	}
	if !filepath.IsAbs(dataHome) {
		return Paths{}, fmt.Errorf("desktop: data directory %q is not absolute", dataHome)
	}
	return PathsFor(filepath.Clean(dataHome)), nil
}

func regularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}

// writeFileAtomic writes data to path through a temp file in the same
// directory and a rename, so a reader never sees a partial file. It skips
// the write when path already holds exactly data.
func writeFileAtomic(path string, data []byte) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("desktop: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("desktop: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("desktop: write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("desktop: chmod %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("desktop: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("desktop: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("desktop: rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// refreshCaches runs gtk-update-icon-cache and update-desktop-database when
// they are on PATH. Errors and timeouts are ignored.
func refreshCaches(opts Options, p Paths) {
	runner := opts.Runner
	if runner == nil {
		runner = proc.ExecRunner{}
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	timeout := opts.RefreshTimeout
	if timeout <= 0 {
		timeout = DefaultRefreshTimeout
	}
	cmds := [][]string{
		{"gtk-update-icon-cache", "-f", "-t", p.HicolorDir},
		{"update-desktop-database", p.ApplicationsDir},
	}
	for _, c := range cmds {
		bin, err := lookPath(c[0])
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, _, _ = runner.Run(ctx, bin, c[1:]...)
		cancel()
	}
}
