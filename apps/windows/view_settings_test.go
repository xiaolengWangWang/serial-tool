//go:build windows

package main

import "testing"

func TestViewSettingsPersistAndKeepNavigation(t *testing.T) {
	a := newWorkbenchForTest(t)
	v := defaultViewSettings()
	v.Connection, v.Send, v.Log = false, false, false
	if err := a.applyViewSettings(v); err != nil {
		t.Fatal(err)
	}
	if a.connectionPane.Visible() || a.sendPane.Visible() || a.viewLog.Visible() {
		t.Fatal("hidden sections remain visible")
	}
	if !a.connectionPageButton.Visible() {
		t.Fatal("hidden connection settings cannot be reopened")
	}
	if got := loadViewSettings(a.engine); got != v {
		t.Fatalf("settings not persisted: %+v", got)
	}
	if err := a.applyViewSettings(viewSettings{}); err == nil {
		t.Fatal("accepted an empty workspace")
	}
}

func TestViewSettingsSinglePaneNavigation(t *testing.T) {
	a := newWorkbenchForTest(t)
	for _, v := range []viewSettings{{Connection: true}, {Data: true}, {Log: true}, {Send: true}, {AI: true}} {
		if err := a.applyViewSettings(v); err != nil {
			t.Fatal(err)
		}
		if !a.connectionPane.Visible() && !a.monitorPane.Visible() && !a.assistant.panel.Visible() {
			t.Fatalf("empty workspace: %+v", v)
		}
	}
	a.narrow = true
	a.toggleAssistant()
	if !a.connectionPane.Visible() {
		t.Fatal("closing the only pane left blank workspace")
	}
	a.showDataPage()
	if !a.assistant.panel.Visible() {
		t.Fatal("cannot return to AI-only view")
	}
	a.showConnectionPage()
	if !a.connectionPane.Visible() || a.assistant.panel.Visible() {
		t.Fatal("connection navigation did not switch away from AI")
	}
}
