package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEventOmitempty checks that zero-value optional fields are omitted: an
// approval-kind event carries no state key. This keeps the wire format
// consistent with the TS type declarations, which mark these fields
// optional with omitempty tags.
func TestEventOmitempty(t *testing.T) {
	t.Parallel()

	stateEv := Event{WorkspaceID: "ws-1", Kind: "state", State: StateRunning}
	data, err := json.Marshal(stateEv)
	if err != nil {
		t.Fatalf("json.Marshal(state event): %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"state":"running"`) {
		t.Errorf("state event must carry state key; got %s", s)
	}

	approvalEv := Event{WorkspaceID: "ws-1", Kind: "approval"}
	data, err = json.Marshal(approvalEv)
	if err != nil {
		t.Fatalf("json.Marshal(approval event): %v", err)
	}
	s = string(data)
	if strings.Contains(s, `"state"`) {
		t.Errorf("approval event with no state must not carry state key; got %s", s)
	}
}

func TestJSONTagsCaps(t *testing.T) {
	c := Caps{Approvals: true, Attention: false}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal(Caps) error: %v", err)
	}
	s := string(data)

	for _, want := range []string{"approvals", "attention"} {
		if !strings.Contains(s, want) {
			t.Errorf("Caps JSON missing lowercase key %q in %s", want, s)
		}
	}
	for _, bad := range []string{"Approvals", "Attention"} {
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

// TestJSONTagsApprovalReq_InputHashOmitempty pins the `inputHash,omitempty`
// contract: an empty InputHash must NOT appear on the wire, so an absent
// hash reads as "no hash" on the TS side, matching the optional field. A
// set InputHash must appear. maybeAutoApprove fails closed on a missing
// hash, so the omitempty behavior is part of the security contract, not
// just a cosmetic choice.
func TestJSONTagsApprovalReq_InputHashOmitempty(t *testing.T) {
	t.Parallel()

	empty, err := json.Marshal(ApprovalReq{ReqID: "r1", Tool: "Bash"})
	if err != nil {
		t.Fatalf("json.Marshal(empty InputHash): %v", err)
	}
	if strings.Contains(string(empty), "inputHash") {
		t.Errorf("empty InputHash must be omitted (omitempty); got %s", empty)
	}

	set, err := json.Marshal(ApprovalReq{ReqID: "r1", Tool: "Bash", InputHash: "deadbeef"})
	if err != nil {
		t.Fatalf("json.Marshal(set InputHash): %v", err)
	}
	if !strings.Contains(string(set), `"inputHash":"deadbeef"`) {
		t.Errorf("set InputHash must be present; got %s", set)
	}
}
