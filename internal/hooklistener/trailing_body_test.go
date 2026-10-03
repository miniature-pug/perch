package hooklistener_test

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/miniature-pug/perch/internal/hooklistener"
)

// TestPermissionRequest_TrailingBodyStillCancels: net/http only notices a
// client disconnect once the body is read to EOF. A JSON body followed by
// extra bytes (curl --data-binary keeps a trailing newline) must still
// produce the cancellation event when the client goes away.
func TestPermissionRequest_TrailingBodyStillCancels(t *testing.T) {
	l := newListener(t)
	body := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"` +
		strings.Repeat("x", 426) + `"}}` + "\n" // the JSON value is exactly 512 bytes: one decoder read
	conn, err := net.Dial("tcp", l.Addr())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "POST /hook HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		l.Token(), len(body), body)
	ev := nextEvent(t, l)
	_ = conn.Close()
	got := nextEvent(t, l)
	if got.Type != hooklistener.EventRequestCancelled || got.ReqID != ev.ReqID {
		t.Fatalf("want cancellation for %s, got %+v", ev.ReqID, got)
	}
}
