// internal/notify/notify_test.go
package notify_test

import (
	"github.com/Miniature-Pug/perch/internal/notify"
	"testing"
)

func TestFakeNotifierRecordsCalls(t *testing.T) {
	t.Parallel()
	f := &notify.FakeNotifier{}
	_ = f.Notify("t1", "b1")
	_ = f.Notify("t2", "b2")
	if len(f.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(f.Calls))
	}
	if f.Calls[0].Title != "t1" || f.Calls[0].Body != "b1" {
		t.Errorf("call[0]: %+v", f.Calls[0])
	}
}

func TestRunnerSeamFallback(t *testing.T) {
	t.Parallel()
	var got []string
	n := notify.NewWithRunner(func(name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	})
	_ = n.Notify("hello", "world")
	if len(got) < 3 || got[0] != "notify-send" || got[1] != "hello" || got[2] != "world" {
		t.Errorf("unexpected argv: %v", got)
	}
}
