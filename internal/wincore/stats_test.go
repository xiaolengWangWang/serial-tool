package wincore

import (
	"net"
	"testing"
	"time"
)

func TestListeningStatsTracksRealPeers(t *testing.T) {
	e, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Connect(Config{Mode: ModeTCPServer, Address: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	s := e.Stats()
	if !s.Listening || s.PeerCount != 0 || s.State != StateConnected {
		t.Fatalf("listener: %+v", s)
	}
	client, err := net.Dial("tcp", s.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	waitPeers := func(n int) Stats {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if s := e.Stats(); s.PeerCount == n {
				return s
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("peer count did not become %d: %+v", n, e.Stats())
		return Stats{}
	}
	s = waitPeers(1)
	if len(s.Peers) != 1 || s.Peers[0] != client.LocalAddr().String() {
		t.Fatalf("peer: %+v", s)
	}
	client.Close()
	s = waitPeers(0)
	if !s.Listening || s.State != StateConnected {
		t.Fatalf("client close stopped listener: %+v", s)
	}
	e.Disconnect()
	s = e.Stats()
	if s.Listening || s.Endpoint != "" || s.PeerCount != 0 {
		t.Fatalf("stale state: %+v", s)
	}
}

func TestFailedConnectReportsErrorState(t *testing.T) {
	e, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Connect(Config{Mode: ModeTCPClient}); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	if s := e.Stats(); s.State != StateError || s.Errors != 1 {
		t.Fatalf("failed connection status = %+v", s)
	}
	e.Disconnect()
	if e.Stats().State != StateDisconnected {
		t.Fatal("disconnect did not clear error state")
	}
}
