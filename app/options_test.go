package app

import "testing"

func TestFileDropOptions_EnablesNativeAbsolutePaths(t *testing.T) {
	// Compile-time/config assertion for the native file-drop wiring. Runtime
	// verification (an OS file-manager drop delivering an absolute path) requires
	// a real WebKitGTK window and is a manual smoke item.
	dd := fileDropOptions()
	if !dd.EnableFileDrop {
		t.Error("EnableFileDrop must be true so OS file drops deliver ABSOLUTE paths via the runtime OnFileDrop event")
	}
	if dd.DisableWebViewDrop {
		t.Error("DisableWebViewDrop must be false: gtk_drag_dest_unset would stop the native drag-data-received/drag-drop signals from firing")
	}
}
