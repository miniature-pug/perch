//go:build integration

package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFakeAgent_PostsHooksAndPrintsLines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "fake-agent")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/Miniature-Pug/perch/cmd/fake-agent").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	var got struct {
		types []string
		mu    sync.Mutex
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev struct {
			HookEventName string `json:"hook_event_name"`
		}
		_ = json.Unmarshal(body, &ev)
		got.mu.Lock()
		got.types = append(got.types, ev.HookEventName)
		got.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)
	}))
	t.Cleanup(srv.Close)

	var stdoutBuf strings.Builder
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"PERCH_HOOK_URL="+srv.URL+"/hook",
		"PERCH_HOOK_TOKEN=test-token",
		"PERCH_SESSION_ID=ses_fake01",
		"PERCH_SCRIPT=SessionStart;PreToolUse,tool=Write,input={};Stop",
		"PERCH_LINES=hello from fake agent|diff --git a/f b/f",
	)
	cmd.Stdout = &stdoutBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("timeout")
	}

	stdout := stdoutBuf.String()
	for _, want := range []string{"hello from fake agent", "diff --git a/f b/f"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q", want)
		}
	}
	got.mu.Lock()
	types := append([]string(nil), got.types...)
	got.mu.Unlock()
	for i, ev := range []string{"SessionStart", "PreToolUse", "Stop"} {
		if i >= len(types) || types[i] != ev {
			t.Errorf("hook[%d]: got %v, want %q", i, types, ev)
		}
	}
}
