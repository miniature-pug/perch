package notify

import "testing"

// TestNotifySendArgs_EndsOptions: the real notify-send exec gets "--"
// before title and body, so a body starting with "-" stays positional.
func TestNotifySendArgs_EndsOptions(t *testing.T) {
	got := notifySendArgs([]string{"Agent error", "-x: provider said no"})
	if len(got) != 3 || got[0] != "--" || got[2] != "-x: provider said no" {
		t.Errorf("notifySendArgs = %q", got)
	}
}
