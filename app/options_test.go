package app

import "testing"

func TestFileDropOptions_EnablesNativeAbsolutePaths(t *testing.T) {
	// This test makes a compile-time and config assertion for the native
	// file-drop wiring. Runtime verification needs a real WebKitGTK window,
	// where an OS file manager drops a file and delivers an absolute path.
	// This verification is a manual smoke item.
	dd := fileDropOptions()
	if !dd.EnableFileDrop {
		t.Error("EnableFileDrop must be true so OS file drops deliver ABSOLUTE paths via the runtime OnFileDrop event")
	}
	if dd.DisableWebViewDrop {
		t.Error("DisableWebViewDrop must be false: gtk_drag_dest_unset would stop the native drag-data-received/drag-drop signals from firing")
	}
}
