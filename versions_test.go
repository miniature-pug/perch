package perch

import (
	"strings"
	"testing"
)

func TestToolVersionsEmbedded(t *testing.T) {
	if len(ToolVersions) == 0 {
		t.Fatal("ToolVersions is empty: embed directive did not capture .tool-versions")
	}
	if !strings.Contains(ToolVersions, "golang") {
		t.Fatalf("ToolVersions does not contain 'golang' entry; got: %q", ToolVersions)
	}
}
