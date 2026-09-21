//go:build windows

package wincore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestVirtualCOMLivePairs(t *testing.T) {
	if os.Getenv("COMMBOX_VIRTUALCOM_TEST") != "1" {
		t.Skip("set COMMBOX_VIRTUALCOM_TEST=1 to build and run the real VirtualCOM provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := filepath.Join(t.TempDir(), "virtualcom-fixture.exe")
	build := exec.CommandContext(ctx, "go", "build", "-o", fixture, "./tests/commbox-fixture")
	build.Dir = filepath.Join("..", "..", "virtualcom")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(ctx, fixture)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pairs [][2]string
	if err := json.NewDecoder(stdout).Decode(&pairs); err != nil {
		stdin.Close()
		cmd.Wait()
		t.Fatalf("provider readiness: %v; %s", err, stderr.String())
	}
	stopped := false
	stopProvider := func() {
		if stopped {
			return
		}
		stopped = true
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("provider exit: %v; %s", err, stderr.String())
		}
	}
	// Register before engine cleanup: client handles close first on failures.
	t.Cleanup(stopProvider)
	if len(pairs) != 2 {
		t.Fatalf("provider pairs: %v", pairs)
	}
	ports, err := ListPorts()
	if err != nil {
		t.Fatal(err)
	}
	engines := make([]*Engine, 0, 4)
	inboxes := make([]chan []byte, 0, 4)
	names := make([]string, 0, 4)
	for _, pair := range pairs {
		for _, name := range pair {
			if !slices.Contains(ports, name) {
				t.Fatalf("real provider port %s not discovered", name)
			}
			rx := make(chan []byte, 512)
			e, err := New(t.TempDir(), func(_ string, data []byte) { rx <- bytes.Clone(data) }, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(e.Close)
			if err := e.Connect(Config{Mode: ModeSerial, SerialName: name, Baud: 9600, DataBits: 8, StopBits: 1}); err != nil {
				t.Fatal(err)
			}
			if e.Stats().SerialNote == "" {
				t.Fatal("byte-stream mode has no compatibility notice")
			}
			engines = append(engines, e)
			inboxes = append(inboxes, rx)
			names = append(names, name)
		}
	}
	t.Logf("real provider pid %d, pairs %v", cmd.Process.Pid, pairs)
	// Four concurrent 1 MiB writes test full duplex and pair isolation. Each
	// direction has a distinct payload containing all 256 possible byte values.
	payloads := make([][]byte, 4)
	sent := make(chan error, 4)
	for i, e := range engines {
		b := make([]byte, 1<<20)
		for j := range b {
			b[j] = byte(j + i*17)
		}
		payloads[i] = b
		go func() { sent <- e.Send(string(b), false, "无") }()
	}
	for i, inbox := range inboxes {
		want := payloads[i^1]
		got := collectVirtualCOMBytes(t, inbox, len(want))
		if !bytes.Equal(got, want) {
			t.Fatalf("%s received changed or crossed bytes", names[i])
		}
	}
	for range engines {
		select {
		case err := <-sent:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("send timed out")
		}
	}
	// Reopen the same port repeatedly; the provider needs a brief reset after
	// the old client closes, so only retry within a bounded deadline.
	for i := 0; i < 5; i++ {
		engines[0].Disconnect()
		deadline := time.Now().Add(2 * time.Second)
		for {
			err := engines[0].Connect(Config{Mode: ModeSerial, SerialName: names[0], Baud: 115200, DataBits: 8, StopBits: 1})
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("reopen: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		message := fmt.Sprintf("reopened-%d", i)
		if err := engines[0].Send(message, false, "无"); err != nil {
			t.Fatal(err)
		}
		if got := collectVirtualCOMBytes(t, inboxes[1], len(message)); string(got) != message {
			t.Fatalf("reopen bytes %q", got)
		}
	}
	// CLI uses the same port entrypoint with no serial control API calls.
	engines[0].Disconnect()
	var client io.ReadWriteCloser
	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err = OpenSerialPort(`\\.\`+names[0], serialMode(Config{Baud: 9600, DataBits: 8, StopBits: 1}))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { client.Close() })
	if _, err := client.Write([]byte("CLI")); err != nil {
		t.Fatal(err)
	}
	if got := collectVirtualCOMBytes(t, inboxes[1], 3); string(got) != "CLI" {
		t.Fatalf("CLI bytes %q", got)
	}
	stopProvider()
	ports, err = ListPorts()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if slices.Contains(ports, name) {
			t.Fatalf("provider did not clean up %s", name)
		}
	}
}

func collectVirtualCOMBytes(t *testing.T, inbox <-chan []byte, size int) []byte {
	t.Helper()
	var got []byte
	deadline := time.After(10 * time.Second)
	for len(got) < size {
		select {
		case b := <-inbox:
			got = append(got, b...)
		case <-deadline:
			t.Fatalf("received %d/%d bytes", len(got), size)
		}
	}
	return got
}
