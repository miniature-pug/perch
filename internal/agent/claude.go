package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Miniature-Pug/perch/internal/model"
)

// Claude is the Adapter for Anthropic's claude-code CLI. It reads claude's
// on-disk transcript store directly rather than shelling out, so session
// enumeration needs no subprocess. All filesystem and PATH access is funneled
// through injected seams (Exists, LookPath) so unit tests touch neither.
type Claude struct {
	// Home is the claude config directory (<claudeHome>). Default:
	// $CLAUDE_CONFIG_DIR, else $HOME/.claude. CLAUDE_CONFIG_DIR is the
	// claude-code override (confirmed present in the 2.1.x binary; it is not
	// listed in --help output).
	Home string
	// Bin is the claude binary name or path used by Detect. Default "claude".
	Bin string
	// Exists probes whether a path exists and is a directory; used by slug
	// decode. Default: os.Stat + IsDir.
	Exists func(path string) bool
	// LookPath resolves a binary on PATH; used by Detect. Default exec.LookPath.
	LookPath func(string) (string, error)
}

// Compile-time guarantee that Claude satisfies the Adapter interface, including
// for the zero value (methods must not panic on nil seams).
var _ Adapter = Claude{}

// NewClaude returns a Claude with all production defaults filled in. It is the
// supported constructor; methods also fall back to defaults internally so a
// partially-constructed or zero Claude never panics.
func NewClaude() Claude {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".claude")
		}
	}
	return Claude{
		Home:     home,
		Bin:      "claude",
		Exists:   defaultExists,
		LookPath: exec.LookPath,
	}
}

// defaultExists reports whether path exists and is a directory.
func defaultExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (c Claude) exists() func(string) bool {
	if c.Exists != nil {
		return c.Exists
	}
	return defaultExists
}

func (c Claude) lookPath() func(string) (string, error) {
	if c.LookPath != nil {
		return c.LookPath
	}
	return exec.LookPath
}

func (c Claude) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "claude"
}

// Name returns the canonical tool identifier.
func (c Claude) Name() string { return string(model.ToolClaude) }

// Detect reports whether the claude binary resolves on PATH.
func (c Claude) Detect() bool {
	_, err := c.lookPath()(c.bin())
	return err == nil
}

// ResumeArgs returns the args to resume sessionID in the current directory.
func (c Claude) ResumeArgs(sessionID string) []string {
	return []string{"--resume", sessionID}
}

// ForkInto returns the args to fork sessionID into a new session. claude forks
// natively with --fork-session, so no transcript copying is needed. targetDir
// is informational for claude: the launch layer applies it as the process cwd
// (this method only builds args), so it is accepted but not encoded here.
func (c Claude) ForkInto(sessionID, targetDir string) ([]string, error) {
	return []string{"--resume", sessionID, "--fork-session"}, nil
}

// NewArgs builds the launch args for a fresh session. opts.Agent is ignored:
// claude has no agent concept (that is an opencode flag).
func (c Claude) NewArgs(opts NewOpts) []string {
	var args []string
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if opts.Prompt != "" {
		// Prompt is a trailing positional argument for claude.
		args = append(args, opts.Prompt)
	}
	return args
}

// ── slug decode ───────────────────────────────────────────────────────────────

// decodeSlug reverses claude's lossy directory slug back to an absolute path.
// claude names each project dir by replacing every "/" in the absolute path
// with "-", which is ambiguous because "-" also appears literally in path
// components. We resolve the ambiguity greedily from the left using exists as
// an oracle: at each position, try the LONGEST joined component first so a real
// "foo-bar" directory beats the "foo"/"bar" split.
//
// Known limitation: this is greedy with no backtracking, so an unrelated
// "a-b" directory that happens to exist can over-merge two real components.
// This is inherent to the lossy slug; the degradation is graceful (a wrong but
// plausible path) and adding backtracking is deliberately out of scope.
func decodeSlug(slug string, exists func(string) bool) string {
	// Defensive: a real slug always starts with "-" (leading "/" of an abs path).
	if !strings.HasPrefix(slug, "-") {
		return slug
	}

	tokens := strings.Split(slug[1:], "-")
	cur := ""
	idx := 0
	for idx < len(tokens) {
		matched := false
		// Largest-k-first: prefer the longest component that exists.
		for k := len(tokens) - 1; k >= idx; k-- {
			// Literal concat (not filepath.Join): with cur=="" Join drops the
			// leading slash and silently breaks the absolute path.
			cand := cur + "/" + strings.Join(tokens[idx:k+1], "-")
			if exists(cand) {
				cur = cand
				idx = k + 1
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}

	if idx == len(tokens) {
		return cur
	}
	// Stuck: longest existing prefix plus the naive "/"-split of the remainder.
	return cur + "/" + strings.Join(tokens[idx:], "/")
}

// ── transcript parsing ──────────────────────────────────────────────────────────

// transcriptRecord is the light projection of a JSONL line we care about. The
// session id is taken from the filename by the enumerator, not from here.
type transcriptRecord struct {
	Type    string        `json:"type"`
	AITitle string        `json:"aiTitle"`
	Cwd     string        `json:"cwd"`
	IsMeta  bool          `json:"isMeta"`
	Message *messageField `json:"message"`
}

type messageField struct {
	// Content is a string OR an array of content blocks; decoded lazily.
	Content json.RawMessage `json:"content"`
}

// scannerBufMax bounds bufio.Scanner's token size. Transcripts can carry very
// long lines (large tool outputs, base64 images), so the default 64 KiB limit
// would trip bufio.ErrTooLong; 8 MiB comfortably covers real records.
const scannerBufMax = 8 * 1024 * 1024

// parseTranscript scans a claude transcript and extracts display metadata.
// title = last ai-title seen, else first non-meta human message text, else "".
// firstCwd = first cwd field seen on any record (assistant/system/user carry it;
// control records do not). hadRecords reports whether any line parsed into a
// valid record, used so an empty file yields no session. A malformed line is
// skipped, never fatal; only a real I/O/scanner error is returned.
func parseTranscript(r io.Reader) (title, firstCwd string, hadRecords bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scannerBufMax)

	var lastAITitle, firstHuman string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec transcriptRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			// Malformed line: skip and continue (fault-tolerance contract).
			continue
		}
		hadRecords = true

		if firstCwd == "" && rec.Cwd != "" {
			firstCwd = rec.Cwd
		}
		if rec.Type == "ai-title" && rec.AITitle != "" {
			lastAITitle = rec.AITitle
		}
		if rec.Type == "user" && firstHuman == "" && !rec.IsMeta {
			if text := humanText(rec.Message); text != "" {
				firstHuman = text
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", hadRecords, err
	}

	title = lastAITitle
	if title == "" {
		title = firstHuman
	}
	return title, firstCwd, hadRecords, nil
}

// humanText extracts readable prompt text from a user message, returning "" for
// synthetic prompts. message.content is either a plain string or an array of
// content blocks; for the array form we take the first block's "text" field.
// Synthetic prompts wrapped in angle-bracket tags (<command-name>, <local-...>)
// are not human input and are rejected.
func humanText(m *messageField) string {
	if m == nil || len(m.Content) == 0 {
		return ""
	}

	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return rejectSynthetic(s)
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Text != "" {
				return rejectSynthetic(b.Text)
			}
		}
	}
	return ""
}

// rejectSynthetic returns "" for command/stdout wrapper text, else the trimmed
// input.
func rejectSynthetic(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

// ── pid trackers ────────────────────────────────────────────────────────────────

// readPidTrackers reads <home>/sessions/*.json and returns sessionId -> cwd.
// Unreadable, malformed, or field-less entries are skipped. A missing sessions
// dir yields an empty map and no error.
func readPidTrackers(home string) map[string]string {
	out := make(map[string]string)
	matches, err := filepath.Glob(filepath.Join(home, "sessions", "*.json"))
	if err != nil {
		return out
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		if rec.SessionID == "" || rec.Cwd == "" {
			continue
		}
		out[rec.SessionID] = rec.Cwd
	}
	return out
}

// ── session enumeration ──────────────────────────────────────────────────────────

// ListSessions enumerates every claude session under <Home>/projects. One bad
// file or directory never aborts the listing; unparseable or empty transcripts
// are skipped and a partial, ID-sorted slice is returned. ctx cancellation is
// honoured between slug directories so the caller can time out the walk.
func (c Claude) ListSessions(ctx context.Context) ([]model.Session, error) {
	exists := c.exists()
	pidMap := readPidTrackers(c.Home)

	// Each immediate child of projects/ is a slug directory. Data dirs named
	// after a session uuid live INSIDE these; we never descend into them and we
	// match only top-level *.jsonl, so sibling subagents/agent-*.jsonl files are
	// never mistaken for sessions.
	slugDirs, err := os.ReadDir(filepath.Join(c.Home, "projects"))
	if err != nil {
		// Missing projects dir = no sessions, not an error.
		return nil, nil
	}

	var sessions []model.Session
	for _, sd := range slugDirs {
		// Honour cancellation between directories so the caller can time out.
		if err := ctx.Err(); err != nil {
			return sessions, err
		}

		if !sd.IsDir() {
			continue
		}
		slug := sd.Name()
		slugPath := filepath.Join(c.Home, "projects", slug)

		transcripts, err := filepath.Glob(filepath.Join(slugPath, "*.jsonl"))
		if err != nil {
			continue
		}
		for _, tp := range transcripts {
			s, ok := c.sessionFromTranscript(tp, slug, exists, pidMap)
			if ok {
				sessions = append(sessions, s)
			}
		}
	}

	// Deterministic order for stable output and tests.
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return sessions, nil
}

// sessionFromTranscript builds one model.Session from a transcript file, or
// reports ok=false to skip it (I/O error or empty file). Directory precedence:
// transcript cwd, else pid-tracker cwd, else slug decode — the transcript is
// authoritative because it records the actual cwd claude ran in; the pid
// tracker is the next-best live signal; slug decode is the lossy last resort.
func (c Claude) sessionFromTranscript(path, slug string, exists func(string) bool, pidMap map[string]string) (model.Session, bool) {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	f, err := os.Open(path)
	if err != nil {
		return model.Session{}, false
	}
	defer func() { _ = f.Close() }()

	title, firstCwd, hadRecords, err := parseTranscript(f)
	if err != nil || !hadRecords {
		// No parseable records (empty file, or every line malformed): the id is
		// known from the filename but the content is unrecoverable, so skip it
		// rather than surface a contentless session.
		return model.Session{}, false
	}

	fi, err := f.Stat()
	if err != nil {
		return model.Session{}, false
	}

	dir := firstCwd
	if dir == "" {
		if pidCwd, ok := pidMap[id]; ok {
			dir = pidCwd
		} else {
			dir = decodeSlug(slug, exists)
		}
	}

	return model.Session{
		ID:        id,
		Tool:      model.ToolClaude,
		Directory: dir,
		Title:     title,
		Updated:   fi.ModTime().Unix(),
	}, true
}
