package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	if password == "" {
		password = "spike3secret"
		os.Setenv("OPENCODE_SERVER_PASSWORD", password)
	}

	projDir, err := os.MkdirTemp("", "spike3-proj-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(projDir)

	// Start opencode serve in the background.
	serveCmd := exec.Command("opencode", "serve")
	serveCmd.Dir = projDir
	serveCmd.Stdout = os.Stdout
	serveCmd.Stderr = os.Stderr
	if err := serveCmd.Start(); err != nil {
		log.Fatal("opencode serve:", err)
	}
	defer serveCmd.Process.Kill()

	// Give the server a moment to bind.
	time.Sleep(2 * time.Second)

	// Discover the URL: opencode serve prints "Listening on http://..." to stderr/stdout.
	// For the spike, hardcode the default or read from env.
	baseURL := os.Getenv("OPENCODE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:4096" // opencode default
	}
	fmt.Println("connecting to:", baseURL)

	// Subscribe to SSE /event in a goroutine; print every frame.
	go func() {
		req, _ := http.NewRequest("GET", baseURL+"/event", nil)
		req.Header.Set("Authorization", "Bearer "+password)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("SSE connect error:", err)
			return
		}
		defer resp.Body.Close()
		fmt.Println("SSE connected; status:", resp.StatusCode)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			fmt.Println("SSE:", line)
			// If a permission.v2.asked event arrives, auto-reply reject after 1s.
			if strings.Contains(line, "permission.v2.asked") {
				var outer struct {
					Event struct {
						Properties struct {
							ID string `json:"id"`
						} `json:"properties"`
					} `json:"event"`
				}
				// data: {"event": ...}
				data := strings.TrimPrefix(line, "data: ")
				if err := json.Unmarshal([]byte(data), &outer); err == nil {
					permID := outer.Event.Properties.ID
					if permID != "" {
						time.Sleep(time.Second)
						replyPermission(baseURL, password, permID, "reject")
					}
				}
			}
		}
	}()

	fmt.Println("opencode serve running; open another terminal and run:")
	fmt.Printf("  opencode attach %s\n", baseURL)
	fmt.Println("then trigger a tool call that requires permission.")
	fmt.Println("press Enter here to stop the spike.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func replyPermission(base, password, id, decision string) {
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req, _ := http.NewRequest("POST", base+"/permission/"+id+"/respond", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("permission reply error:", err)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Printf("permission reply %s → %d %s\n", decision, resp.StatusCode, strings.TrimSpace(string(out)))
}
