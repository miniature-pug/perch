package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const token = "spike2-secret"

var capturedTranscript string

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p struct {
			HookEventName  string `json:"hook_event_name"`
			TranscriptPath string `json:"transcript_path"`
			SessionID      string `json:"session_id"`
		}
		_ = json.Unmarshal(body, &p)
		if p.TranscriptPath != "" && capturedTranscript == "" {
			capturedTranscript = p.TranscriptPath
			fmt.Println("captured transcript_path:", capturedTranscript)
			go tailTranscript(capturedTranscript)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	go func() { _ = http.Serve(ln, mux) }()

	projDir, _ := os.MkdirTemp("", "spike2-proj-*")
	defer os.RemoveAll(projDir)

	claudeDir := filepath.Join(projDir, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)

	// Register Stop hook to capture transcript_path (available on all event types).
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
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
			"SessionStart": []any{
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
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), settingsJSON, 0o644)

	// Derive the expected slug from projDir and print it for manual verification.
	slug := "-" + strings.ReplaceAll(projDir[1:], "/", "-")
	fmt.Println("project dir:", projDir)
	fmt.Println("expected slug:", slug)
	fmt.Println("launching claude — complete one turn, then Ctrl-C")

	cmd := exec.Command("claude")
	cmd.Dir = projDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

type usageRecord struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		CacheRead    int     `json:"cache_read_input_tokens"`
		CacheWrite   int     `json:"cache_creation_input_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	} `json:"usage"`
	CostUSD float64 `json:"costUSD"` // alternate top-level field seen in some versions
}

func tailTranscript(path string) {
	// Poll until the file appears (hook may fire before first write).
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Println("tail: open error:", err)
		return
	}
	defer f.Close()
	// Seek to end, then follow new lines.
	_, _ = f.Seek(0, io.SeekStart)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for {
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var rec usageRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.Usage != nil {
				fmt.Printf("USAGE record (type=%s): in=%d out=%d cache_read=%d cache_write=%d cost_usd=%v top_cost_usd=%v\n",
					rec.Type,
					rec.Usage.InputTokens, rec.Usage.OutputTokens,
					rec.Usage.CacheRead, rec.Usage.CacheWrite,
					rec.Usage.CostUSD, rec.CostUSD)
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Println("tail scan error:", err)
			return
		}
		time.Sleep(300 * time.Millisecond)
		// Reset scanner to continue reading new data appended to the file.
		sc = bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	}
}
