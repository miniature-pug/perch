package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const token = "spike1-secret"

type hookPayload struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr().String()
	fmt.Println("hook listener:", addr)

	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		var p hookPayload
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		fmt.Printf("=== HOOK EVENT ===\n")
		fmt.Printf("hook_event_name : %s\n", p.HookEventName)
		fmt.Printf("session_id      : %s\n", p.SessionID)
		fmt.Printf("transcript_path : %s\n", p.TranscriptPath)
		fmt.Printf("cwd             : %s\n", p.Cwd)
		fmt.Printf("tool_name       : %s\n", p.ToolName)
		fmt.Printf("tool_input      : %s\n", string(p.ToolInput))

		resp := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "deny",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	go func() { _ = http.Serve(ln, mux) }()

	projDir, err := os.MkdirTemp("", "spike1-proj-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(projDir)

	claudeDir := filepath.Join(projDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		log.Fatal(err)
	}

	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type": "http",
							"url":  "http://" + addr + "/hook",
							"headers": map[string]any{
								"Authorization": "Bearer " + token,
							},
						},
					},
				},
			},
		},
	}
	settingsJSON, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsJSON, 0o644); err != nil {
		log.Fatal(err)
	}

	fmt.Println("project dir:", projDir)
	fmt.Println("settings written; launching claude — type a message that triggers a tool call (e.g. 'list files in current dir')")
	fmt.Println("press Ctrl-C to exit spike")

	cmd := exec.Command("claude")
	cmd.Dir = projDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("claude exited:", err)
	}
}
