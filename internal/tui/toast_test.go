package tui

import (
	"testing"
)

func TestWithToast_SetsMessageAndIncrementsSeq(t *testing.T) {
	m := New(nil)
	m, cmd := m.withToast("hello")
	if m.toast != "hello" {
		t.Fatalf("toast = %q, want %q", m.toast, "hello")
	}
	if m.toastSeq != 1 {
		t.Fatalf("toastSeq = %d, want 1", m.toastSeq)
	}
	if cmd == nil {
		t.Fatal("withToast must return a non-nil clear cmd")
	}
}

func TestClearToastMsg_MatchingSeqClears(t *testing.T) {
	m := New(nil)
	m, _ = m.withToast("hi")
	model, _ := m.Update(clearToastMsg{seq: m.toastSeq})
	got := model.(Model)
	if got.toast != "" {
		t.Fatalf("toast = %q, want empty after matching clear", got.toast)
	}
}

func TestClearToastMsg_StaleSeqIgnored(t *testing.T) {
	m := New(nil)
	m, _ = m.withToast("first")
	m, _ = m.withToast("second")
	model, _ := m.Update(clearToastMsg{seq: 1})
	got := model.(Model)
	if got.toast != "second" {
		t.Fatalf("toast = %q, want %q (stale clear must be ignored)", got.toast, "second")
	}
}
