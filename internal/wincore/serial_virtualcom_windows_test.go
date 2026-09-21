//go:build windows

package wincore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Real Windows pipe + DOS alias, with no driver or serial-library mock.
// Only unused high COM numbers are allocated; cleanup matches the exact target.
type virtualCOMFixture struct {
	name   string
	target string
	h      windows.Handle
}

func newVirtualCOMFixture(t *testing.T, prefix string) *virtualCOMFixture {
	t.Helper()
	f := &virtualCOMFixture{h: windows.InvalidHandle}
	for n := 4096; n >= 3000; n-- {
		name := fmt.Sprintf("COM%d", n)
		buf := make([]uint16, 1024)
		_, err := windows.QueryDosDevice(windows.StringToUTF16Ptr(name), &buf[0], uint32(len(buf)))
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			f.name = name
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.name == "" {
		t.Fatal("no unused test COM number")
	}
	pipe := fmt.Sprintf("%s%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
	f.target = `\Device\NamedPipe\` + pipe
	var err error
	f.h, err = windows.CreateNamedPipe(windows.StringToUTF16Ptr(`\\.\pipe\`+pipe),
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.h != windows.InvalidHandle {
			windows.CloseHandle(f.h)
		}
	})
	if err := windows.DefineDosDevice(0x1|0x8, windows.StringToUTF16Ptr(f.name), windows.StringToUTF16Ptr(f.target)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.DefineDosDevice(0x1|0x2|0x4|0x8, windows.StringToUTF16Ptr(f.name), windows.StringToUTF16Ptr(f.target)); err != nil {
			t.Error(err)
		}
	})
	return f
}

func fixtureIO(h windows.Handle, data []byte, write bool) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := &windows.Overlapped{HEvent: event}
	var n uint32
	if write {
		err = windows.WriteFile(h, data, &n, ov)
	} else {
		err = windows.ReadFile(h, data, &n, ov)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		status, waitErr := windows.WaitForSingleObject(event, 3000)
		if waitErr != nil || status != windows.WAIT_OBJECT_0 {
			windows.CancelIoEx(h, ov)
			windows.GetOverlappedResult(h, ov, &n, true)
			return int(n), fmt.Errorf("fixture I/O timeout: %v", waitErr)
		}
		err = windows.GetOverlappedResult(h, ov, &n, true)
	}
	return int(n), err
}

func virtualCOMEngine(t *testing.T, f *virtualCOMFixture, received chan []byte) *Engine {
	t.Helper()
	e, err := New(t.TempDir(), func(_ string, b []byte) {
		if received != nil {
			received <- bytes.Clone(b)
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err := e.Connect(Config{Mode: ModeSerial, SerialName: strings.ToLower(f.name), Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatalf("CommBox could not open VirtualCOM: %v", err)
	}
	return e
}

func TestVirtualCOMDiscoveryDoesNotOccupyPort(t *testing.T) {
	f := newVirtualCOMFixture(t, "VirtualCOM-")
	for i := 0; i < 2; i++ {
		ports, err := ListPorts()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ports, f.name) {
			t.Fatalf("VirtualCOM %s missing from %v", f.name, ports)
		}
	}
	virtualCOMEngine(t, f, nil)
}

func TestVirtualCOMEngineFullDuplexAndCapture(t *testing.T) {
	f := newVirtualCOMFixture(t, "VirtualCOM-")
	rx := make(chan []byte, 256)
	e := virtualCOMEngine(t, f, rx)
	// A pending read must not block writes on the same handle.
	payload := bytes.Repeat([]byte{0, 1, 0x80, 0xff, '\r', '\n'}, 8192)
	serverRead := make(chan error, 1)
	go func() {
		got := make([]byte, len(payload))
		for offset := 0; offset < len(got); {
			n, err := fixtureIO(f.h, got[offset:], false)
			if err != nil {
				serverRead <- err
				return
			}
			if n == 0 {
				serverRead <- errors.New("zero read")
				return
			}
			offset += n
		}
		if !bytes.Equal(got, payload) {
			serverRead <- errors.New("outbound bytes changed")
			return
		}
		serverRead <- nil
	}()
	sent := make(chan error, 1)
	go func() { sent <- e.Send(string(payload), false, "无") }()
	if n, err := fixtureIO(f.h, payload, true); err != nil || n != len(payload) {
		t.Fatalf("server write: %d, %v", n, err)
	}
	var got []byte
	deadline := time.After(3 * time.Second)
	for len(got) < len(payload) {
		select {
		case b := <-rx:
			got = append(got, b...)
		case <-deadline:
			t.Fatal("receive timeout")
		}
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("inbound bytes changed")
	}
	for _, done := range []chan error{serverRead, sent} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("send timeout")
		}
	}
	var stored int
	if err := e.store.db.QueryRow("SELECT sum(length(raw_data)) FROM received_data WHERE direction = 'RX'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != len(payload) {
		t.Fatalf("stored RX = %d, want %d", stored, len(payload))
	}
}

func TestVirtualCOMCloseCancelsPendingReadAndWrite(t *testing.T) {
	f := newVirtualCOMFixture(t, "VirtualCOM-")
	e := virtualCOMEngine(t, f, nil)
	sent := make(chan error, 1)
	go func() { sent <- e.Send(strings.Repeat("x", 1<<20), false, "无") }()
	// Wait until Windows has accepted data; the unread large write is pending.
	buf := make([]byte, 1)
	if _, err := fixtureIO(f.h, buf, false); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { e.Disconnect(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect blocked on pending I/O")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("cancelled write reported full success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write was not cancelled")
	}
	if err := e.Send("later", false, "无"); err == nil {
		t.Fatal("disconnected port accepted data")
	}
}

func TestVirtualCOMBusyAndProviderExit(t *testing.T) {
	f := newVirtualCOMFixture(t, "VirtualCOM-")
	e := virtualCOMEngine(t, f, nil)
	other, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	err = other.Connect(Config{Mode: ModeSerial, SerialName: f.name, Baud: 9600, DataBits: 8, StopBits: 1})
	if err == nil || !strings.Contains(err.Error(), "占用") {
		t.Fatalf("busy error: %v", err)
	}
	closed := make(chan struct{}, 1)
	e.SetOnClosed(func() { closed <- struct{}{} })
	windows.CloseHandle(f.h)
	f.h = windows.InvalidHandle
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("provider exit not reported")
	}
	if e.Stats().State != StateDisconnected {
		t.Fatalf("provider exited but state = %v", e.Stats().State)
	}
	ports, err := ListPorts()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ports, f.name) {
		t.Fatal("stale COM alias advertised as available")
	}
}

func TestVirtualCOMDoesNotAdaptUnrelatedPipes(t *testing.T) {
	f := newVirtualCOMFixture(t, "UnrelatedPipe-")
	ports, err := ListPorts()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ports, f.name) {
		t.Fatal("unrelated pipe listed as VirtualCOM")
	}
	e, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Connect(Config{Mode: ModeSerial, SerialName: f.name, Baud: 9600, DataBits: 8, StopBits: 1}); err == nil {
		t.Fatal("unrelated pipe accepted as serial")
	}
}
