//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestManualEmbeddedAndSearchable(t *testing.T) {
	all := manualChapters("")
	if len(all) < 17 || all[0].title != "CommBox 使用手册" {
		t.Fatalf("missing or unreadable offline manual: %d chapters", len(all))
	}
	for _, query := range []string{"视图设置", "Ctrl+Enter", "30 天", "CLI", "虚拟串口"} {
		if len(manualChapters(query)) == 0 {
			t.Errorf("no results for %s", query)
		}
	}
	if len(manualChapters("there-is-no-such-chapter-999")) != 0 {
		t.Fatal("unexpected match")
	}
	if !strings.Contains(all[len(all)-1].body, "常见问题") {
		t.Fatal("last chapter missing")
	}
}

func TestManualWindowCanReopen(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.showHelp()
	w := a.manualWindow
	if w == nil || !w.Visible() {
		t.Fatal("manual not opened")
	}
	w.Hide()
	a.showHelp()
	if a.manualWindow != w || !w.Visible() {
		t.Fatal("manual was not reused")
	}
	w.SetSuspended(true)
}
