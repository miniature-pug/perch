package app

import (
	"bytes"
	"testing"

	"github.com/miniature-pug/perch/internal/desktop"
)

func TestAppIcon_IsThePNGTheWindowUses(t *testing.T) {
	if !bytes.HasPrefix(AppIcon(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("AppIcon is not a PNG")
	}
	if !bytes.Equal(AppIcon(), appIcon) {
		t.Fatal("AppIcon differs from the embedded window icon")
	}
}

// perch.desktop's StartupWMClass and the Wayland app_id are desktop.AppID, and
// ProgramName is appTitle, so the two must stay equal.
func TestAppTitleIsTheDesktopAppID(t *testing.T) {
	if appTitle != desktop.AppID {
		t.Fatalf("appTitle = %q, desktop.AppID = %q", appTitle, desktop.AppID)
	}
}
