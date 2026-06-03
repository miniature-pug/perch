package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONTagsCaps(t *testing.T) {
	c := Caps{Approvals: true, Attention: false, Tokens: true}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal(Caps) error: %v", err)
	}
	s := string(data)

	for _, want := range []string{"approvals", "attention", "tokens"} {
		if !strings.Contains(s, want) {
			t.Errorf("Caps JSON missing lowercase key %q in %s", want, s)
		}
	}
	for _, bad := range []string{"Approvals", "Attention", "Tokens"} {
		if strings.Contains(s, bad) {
			t.Errorf("Caps JSON has capitalized key %q in %s", bad, s)
		}
	}
}

func TestJSONTagsApprovalReq(t *testing.T) {
	a := ApprovalReq{ReqID: "r1", Tool: "Bash", Summary: "ls -la"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("json.Marshal(ApprovalReq) error: %v", err)
	}
	s := string(data)

	for _, want := range []string{"reqId", "tool", "summary"} {
		if !strings.Contains(s, want) {
			t.Errorf("ApprovalReq JSON missing lowercase key %q in %s", want, s)
		}
	}
	for _, bad := range []string{"ReqID", "Tool", "Summary"} {
		if strings.Contains(s, bad) {
			t.Errorf("ApprovalReq JSON has capitalized key %q in %s", bad, s)
		}
	}
}
