package ui

import (
	"strings"
	"testing"
)

func TestTabBarStartsAtEditorLeftEdge(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	lines := m.renderTabBar(r.tabbar)
	if len(lines) == 0 {
		t.Fatal("no tab bar rendered")
	}
	line := stripANSI(lines[0])
	if !strings.HasPrefix(line, " "+m.tabLabel(0)) {
		t.Fatalf("the first tab must start at the editor column's left edge:\n%q", line)
	}
}
