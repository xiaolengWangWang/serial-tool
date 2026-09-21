//go:build windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"serial-tool/internal/wincore"
)

// These tests create real Win32 controls; opt in on an interactive Windows host.
func newWorkbenchForTest(t *testing.T) *application {
	t.Helper()
	if os.Getenv("COMMBOX_GUI_TEST") != "1" {
		t.Skip("set COMMBOX_GUI_TEST=1 to test real Windows controls")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	e, err := wincore.New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	a := &application{engine: e, packetModel: new(packetTableModel)}
	if err := a.createWindow(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// These control tests do not run the normal message loop. Let Walk finish
		// its asynchronous layout before closing the window's layout channels.
		a.mw.SetSuspended(true)
		time.Sleep(200 * time.Millisecond)
		a.mw.Dispose()
	})
	a.updateMode()
	return a
}

func TestWorkbenchHTTPModePreservesURLAndSendsRequest(t *testing.T) {
	a := newWorkbenchForTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	a.mode.SetCurrentIndex(4)
	a.netIP.SetText(srv.URL)
	a.netPort.SetText("9000") // Hidden value left over from TCP.
	cfg, err := a.config()
	if err != nil || cfg.Address != srv.URL {
		t.Fatalf("HTTP URL was altered: config=%+v err=%v", cfg, err)
	}
	if a.hexSend.Checked() || a.hexSend.Enabled() {
		t.Fatal("HTTP mode must use text, not HEX")
	}
	if err := a.engine.Connect(cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.sendCaptured("GET /health", a.hexSend.Checked(), a.eol.Text(), "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestWorkbenchNetworkModeIgnoresHiddenSerialFields(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.baud.SetText("invalid")
	if _, err := a.config(); err != nil {
		t.Fatalf("TCP was blocked by hidden serial field: %v", err)
	}
	a.mode.SetCurrentIndex(0)
	if _, err := a.config(); err == nil {
		t.Fatal("serial mode accepted invalid baud rate")
	}
}

func TestWorkbenchVirtualCOMHistoryKeepsUsableDefaults(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.recentSessions = []wincore.SessionInfo{{Mode: string(wincore.ModeSerial), Endpoint: "COM10", Parameters: "serial=COM10,backend=VirtualCOM,protocol=,role="}}
	a.recentConn.SetModel([]string{"串口 COM10"})
	a.recentConn.SetCurrentIndex(0)
	a.onRecentConnSelected()
	cfg, err := a.config()
	if err != nil || cfg.SerialName != "COM10" || cfg.Baud != 115200 || cfg.DataBits != 8 {
		t.Fatalf("VirtualCOM history damaged serial defaults: %+v, %v", cfg, err)
	}
}

func TestWorkbenchVirtualCOMStatusExplainsIgnoredParameters(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.mode.SetCurrentIndex(0)
	a.updateStatus(wincore.Stats{Mode: wincore.ModeSerial, State: wincore.StateConnected, Endpoint: "COM10", SerialNote: "串口参数不生效"})
	if !strings.Contains(a.status.Text(), "免驱动") || !strings.Contains(a.footer.Text(), "串口参数不生效") {
		t.Fatalf("missing VirtualCOM notice: %q / %q", a.status.Text(), a.footer.Text())
	}
	a.updateStatus(wincore.Stats{Mode: wincore.ModeSerial, State: wincore.StateConnected, Endpoint: "COM1"})
	if strings.Contains(a.status.Text(), "免驱动") || strings.Contains(a.footer.Text(), "VirtualCOM") {
		t.Fatal("physical port retained VirtualCOM notice")
	}
}

func TestWorkbenchHTTPModeRestoresSendFormat(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.hexSend.SetChecked(true)
	a.mode.SetCurrentIndex(4)
	if a.hexSend.Checked() {
		t.Fatal("HTTP retained HEX send format")
	}
	a.mode.SetCurrentIndex(1)
	if !a.hexSend.Checked() || !a.hexSend.Enabled() || !a.eol.Enabled() {
		t.Fatal("leaving HTTP did not restore send format")
	}
}

func TestWorkbenchStatisticsDoNotDismissOpenFilter(t *testing.T) {
	a := newWorkbenchForTest(t)
	a.mw.Show()
	win.SendMessage(a.dirFilter.Handle(), win.CB_SHOWDROPDOWN, 1, 0)
	if win.SendMessage(a.dirFilter.Handle(), win.CB_GETDROPPEDSTATE, 0, 0) == 0 {
		t.Fatal("filter did not open")
	}
	a.updateStatus(a.engine.Stats())
	if win.SendMessage(a.dirFilter.Handle(), win.CB_GETDROPPEDSTATE, 0, 0) == 0 {
		t.Fatal("statistics refresh dismissed the user's open filter")
	}
	win.SendMessage(a.dirFilter.Handle(), win.CB_SHOWDROPDOWN, 0, 0)
}

func TestWorkbenchSecondaryWindowsSurviveUnavailableIconCache(t *testing.T) {
	a := newWorkbenchForTest(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", blocked)
	old := toolbarIcons["app"]
	delete(toolbarIcons, "app")
	t.Cleanup(func() { toolbarIcons["app"] = old })
	a.openMonitor()
	a.openToolbox()
	t.Cleanup(func() {
		a.monitorWindow.SetSuspended(true)
		a.toolboxWindow.SetSuspended(true)
		time.Sleep(200 * time.Millisecond)
		a.monitorWindow.Dispose()
		a.toolboxWindow.Dispose()
	})
	if !a.monitorWindow.Visible() || !a.toolboxWindow.Visible() {
		t.Fatal("secondary windows did not open without an icon cache")
	}
}

func TestWorkbenchTraySurvivesUnavailableIconCache(t *testing.T) {
	a := newWorkbenchForTest(t)
	// The app resource is linked into both the executable and this test binary.
	icon, err := walk.NewIconFromResourceIdWithSize(2, walk.Size{Width: 32, Height: 32})
	if err != nil {
		t.Fatalf("embedded application icon: %v", err)
	}
	icon.Dispose()
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", blocked)
	old := toolbarIcons["app"]
	delete(toolbarIcons, "app")
	t.Cleanup(func() { toolbarIcons["app"] = old })
	if err := a.setupTray(); err != nil {
		t.Fatal(err)
	}
	if a.notifyIcon == nil {
		t.Fatal("tray was not created")
	}
	t.Cleanup(func() { a.notifyIcon.Dispose() })
	if a.notifyIcon.Icon() == nil || !a.notifyIcon.Visible() {
		t.Fatal("tray must have a visible icon even when the disk cache is unavailable")
	}
	a.mw.Show()
	win.ShowWindow(a.mw.Handle(), win.SW_MINIMIZE)
	if !win.IsIconic(a.mw.Handle()) {
		t.Fatal("window did not minimize")
	}
	a.restoreMainWindow()
	if win.IsIconic(a.mw.Handle()) || !a.mw.Visible() {
		t.Fatal("restoring the main window must unminimize and show it")
	}
}
