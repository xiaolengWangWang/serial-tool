//go:build windows

package main

import (
	"testing"
	"time"

	"github.com/lxn/walk"
)

func TestApprovedLayoutKeepsThreePanesAndContextByComposer(t *testing.T) {
	a := newWorkbenchForTest(t)
	driveWorkbench(a, func(onUI func(func())) {
		onUI(func() {
			a.mw.SetSize(walk.Size{Width: 1360, Height: 820})
			a.showAssistant()
		})
		time.Sleep(500 * time.Millisecond)
		onUI(func() {
			if !a.connectionPane.Visible() || !a.monitorPane.Visible() || !a.assistant.panel.Visible() {
				t.Error("normal desktop width should show all three panes")
			}
			view := windowRect(a.assistant.chat.view.Handle())
			attach := windowRect(a.assistant.attach.Handle())
			input := windowRect(a.assistant.chat.input.Handle())
			if view.Bottom > attach.Top || attach.Bottom > input.Top {
				t.Error("packet options must sit between the conversation and composer")
			}
		})
	})
}

func TestConnectionLabelsAndRelevantOptions(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.mode.SetCurrentIndex(1)
	a.role.SetCurrentIndex(0)
	if a.addressLabel.Text() != "监听地址" || a.reconnectPane.Visible() {
		t.Fatal("server form is incorrect")
	}
	a.role.SetCurrentIndex(1)
	if a.addressLabel.Text() != "目标地址" {
		t.Fatal("client address label is incorrect")
	}
	a.mode.SetCurrentIndex(4)
	if a.addressLabel.Text() != "基础 URL" || a.netPort.Visible() || a.reconnectPane.Visible() {
		t.Fatal("HTTP form contains socket-only parameters")
	}
	a.mode.SetCurrentIndex(0)
	if a.peerList.Visible() {
		t.Fatal("serial form contains an irrelevant peer list")
	}
}
