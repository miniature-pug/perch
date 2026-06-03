package app

import "testing"

func TestOptions_DisableWebViewDrop_Set(t *testing.T) {
	// Compile-time assertion that the spike-4 mitigation flag exists and is true.
	// (Runtime verification requires a real Wails window — an integration concern.)
	if !disableWebViewDropForSpike4 {
		t.Error("disableWebViewDropForSpike4 must be true (Wails #3686 mitigation)")
	}
}
