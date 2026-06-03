package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	hookURL := mustEnv("PERCH_HOOK_URL")
	token := mustEnv("PERCH_HOOK_TOKEN")
	sessionID := envOr("PERCH_SESSION_ID", "ses_fakedefault")

	if raw := os.Getenv("PERCH_LINES"); raw != "" {
		for _, line := range strings.Split(raw, "|") {
			fmt.Println(line)
		}
	}
	time.Sleep(20 * time.Millisecond)

	for _, descriptor := range strings.Split(envOr("PERCH_SCRIPT", "SessionStart;Stop"), ";") {
		parts := strings.Split(descriptor, ",")
		switch parts[0] {
		case "SessionStart":
			postHook(hookURL, token, map[string]any{"hook_event_name": "SessionStart", "session_id": sessionID})
		case "PreToolUse":
			toolName, inputJSON := "Unknown", "{}"
			for _, kv := range parts[1:] {
				if after, ok := strings.CutPrefix(kv, "tool="); ok {
					toolName = after
				}
				if after, ok := strings.CutPrefix(kv, "input="); ok {
					inputJSON = after
				}
			}
			resp := postHook(hookURL, token, map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      sessionID,
				"tool_name":       toolName,
				"tool_input":      json.RawMessage(inputJSON),
			})
			var dec struct {
				HookSpecificOutput struct {
					PermissionDecision string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			_ = json.Unmarshal(resp, &dec)
			if dec.HookSpecificOutput.PermissionDecision == "deny" {
				fmt.Fprintln(os.Stderr, "fake-agent: denied")
				os.Exit(1)
			}
		case "Stop":
			postHook(hookURL, token, map[string]any{"hook_event_name": "Stop", "session_id": sessionID})
		}
	}
}

func postHook(baseURL, token string, payload map[string]any) []byte {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		die("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		die("POST: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		die("server %d: %s", resp.StatusCode, b)
	}
	return b
}

func mustEnv(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	die("required env var %s unset", k)
	return ""
}
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fake-agent: "+format+"\n", args...)
	os.Exit(2)
}
