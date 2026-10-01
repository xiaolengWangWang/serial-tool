//go:build windows

package main

import (
	"bytes"
	"testing"
	"time"

	virtualcom "github.com/xiaolengWangWang/virtualcom"
	"serial-tool/core"
)

func TestIntegratedVirtualCOMPersistsUntilApplicationExit(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.openVirtualCOM()
	if a.virtualCOMManager == nil || a.virtualCOMWindow == nil {
		t.Fatal("management window was not created")
	}
	mgr := a.virtualCOMManager
	pair, err := mgr.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	a.virtualCOMWindow.SetSuspended(true)
	time.Sleep(200 * time.Millisecond)
	a.virtualCOMWindow.Dispose()
	if len(mgr.List().Pairs) != 1 {
		t.Fatal("closing the page destroyed its ports")
	}
	a.openVirtualCOM()
	if a.virtualCOMManager != mgr {
		t.Fatal("reopening replaced the shared manager")
	}
	peer, err := virtualcom.OpenPort(pair.B.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	inbox := make(chan []byte, 4)
	a.engine.SetOnPacket(func(p core.Packet) {
		if p.Direction == "RX" {
			inbox <- bytes.Clone(p.Data)
		}
	})
	if err := a.engine.Connect(core.Config{Mode: core.ModeSerial, SerialName: pair.A.Name, Baud: 9600, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write([]byte("embedded-port")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-inbox:
		if string(got) != "embedded-port" {
			t.Fatalf("received %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("embedded port did not reach CommBox engine")
	}
	a.engine.Disconnect()
	peer.Close()
	a.shutdownVirtualCOM()
	if len(mgr.List().Pairs) != 0 {
		t.Fatal("application shutdown left pairs registered")
	}
	if client, err := virtualcom.OpenPort(pair.A.Name); err == nil {
		client.Close()
		t.Fatal("port still exists after shutdown")
	}
}
