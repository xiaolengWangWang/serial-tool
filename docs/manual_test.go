package docs

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOfflineManualMatchesSource(t *testing.T) {
	data, err := exec.Command("git", "show", ":docs/CommBox使用手册.md").Output()
	if err != nil {
		t.Fatal(err)
	}
	if Manual != strings.ReplaceAll(string(data), "\r\n", "\n") {
		t.Fatal("offline manual is stale: stage the Markdown and run go generate ./docs")
	}
}
