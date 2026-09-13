package wincore

import "testing"

func TestTargetedInputPreservesHistoryAndRejectsDisconnectedPeer(t *testing.T) {
	e := newTCPServerForTest(t, nil)
	clients := dialTestClients(t, e, 2)
	infos := waitConnections(t, e, 2)
	p := connectionForRemote(t, infos, clients[0].LocalAddr().String())
	if err := e.SendInputToConnection(p.ID, "41 42", true, "无"); err != nil {
		t.Fatal(err)
	}
	readExactly(t, clients[0], "AB")
	if h := e.RecentSends(); len(h) != 1 || h[0] != "41 42" {
		t.Fatalf("history: %v", h)
	}
	if err := e.DisconnectConnection(p.ID); err != nil {
		t.Fatal(err)
	}
	waitConnections(t, e, 1)
	if err := e.SendInputToConnection(p.ID, "stale", false, "无"); err == nil {
		t.Fatal("disconnected target succeeded")
	}
	if h := e.RecentSends(); len(h) != 1 {
		t.Fatalf("failed send recorded: %v", h)
	}
}
