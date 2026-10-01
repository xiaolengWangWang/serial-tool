//go:build windows

package main

import (
	virtualcom "github.com/xiaolengWangWang/virtualcom"
	"github.com/xiaolengWangWang/virtualcom/gui"
	"strings"
)

func (a *application) openVirtualCOM() {
	if a.virtualCOMWindow != nil && !a.virtualCOMWindow.IsDisposed() {
		a.virtualCOMWindow.Show()
		a.virtualCOMWindow.Activate()
		return
	}
	if a.virtualCOMManager == nil {
		removed, err := virtualcom.RemoveStaleLinks()
		if len(removed) > 0 {
			a.appendLog("已清理 VirtualCOM 残留端口: " + strings.Join(removed, ", "))
		}
		if err != nil {
			a.showError(err)
			return
		}
		a.virtualCOMManager = virtualcom.NewManager()
	}
	w, _, err := gui.NewWindow(a.mw, a.virtualCOMManager, a.refreshPorts)
	if err != nil {
		a.showError(err)
		return
	}
	a.virtualCOMWindow = w
	w.Show()
	w.Activate()
}

func (a *application) shutdownVirtualCOM() {
	if a.virtualCOMWindow != nil && !a.virtualCOMWindow.IsDisposed() {
		a.virtualCOMWindow.Dispose()
	}
	a.virtualCOMWindow = nil
	if a.virtualCOMManager != nil {
		a.virtualCOMManager.CloseAll()
		a.virtualCOMManager = nil
	}
}
