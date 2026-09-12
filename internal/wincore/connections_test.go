package wincore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTCPServerForTest(t *testing.T, onData func(string, []byte)) *Engine {
	t.Helper()
	e, err := New(t.TempDir(), onData, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err := e.Connect(Config{Mode: ModeTCPServer, Address: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func waitConnections(t *testing.T, e *Engine, count int) []ConnectionInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := e.Connections(); len(got) == count {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("connections did not become %d: %+v", count, e.Connections())
	return nil
}

func dialTestClients(t *testing.T, e *Engine, count int) []net.Conn {
	t.Helper()
	clients := make([]net.Conn, count)
	for i := range clients {
		client, err := net.DialTimeout("tcp", e.Stats().Endpoint, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = client
		t.Cleanup(func() { _ = client.Close() })
	}
	return clients
}

func readExactly(t *testing.T, conn net.Conn, want string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read %q: %v", want, err)
	}
	if string(got) != want {
		t.Fatalf("read = %q, want %q", got, want)
	}
}

func connectionForRemote(t *testing.T, infos []ConnectionInfo, remote string) ConnectionInfo {
	t.Helper()
	for _, info := range infos {
		if info.RemoteAddress == remote {
			return info
		}
	}
	t.Fatalf("connection for %s not found: %+v", remote, infos)
	return ConnectionInfo{}
}

func TestTCPConnectionIdentityRoutingAndIsolation(t *testing.T) {
	e := newTCPServerForTest(t, nil)
	clients := dialTestClients(t, e, 2)
	infos := waitConnections(t, e, 2)
	first := connectionForRemote(t, infos, clients[0].LocalAddr().String())
	second := connectionForRemote(t, infos, clients[1].LocalAddr().String())
	if first.ID == second.ID || first.RemoteAddress == second.RemoteAddress {
		t.Fatalf("same-IP connections not distinct: %+v %+v", first, second)
	}
	if !strings.HasPrefix(first.ID, "tcp-") || first.Transport != "TCP" || !first.Active {
		t.Fatalf("unexpected connection metadata: %+v", first)
	}

	if err := e.SendToConnection(first.ID, []byte("one")); err != nil {
		t.Fatal(err)
	}
	readExactly(t, clients[0], "one")
	if err := clients[1].SetReadDeadline(time.Now().Add(75 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if n, err := clients[1].Read(make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("targeted send leaked to second connection: n=%d err=%v", n, err)
	}

	if err := e.Send("all", false, "无"); err != nil {
		t.Fatal(err)
	}
	readExactly(t, clients[0], "all")
	readExactly(t, clients[1], "all")

	if err := e.DisconnectConnection(first.ID); err != nil {
		t.Fatal(err)
	}
	remaining := waitConnections(t, e, 1)
	if remaining[0].ID != second.ID {
		t.Fatalf("wrong connection remained: %+v", remaining)
	}
	if err := e.SendToConnection(second.ID, []byte("still-alive")); err != nil {
		t.Fatal(err)
	}
	readExactly(t, clients[1], "still-alive")
	if err := e.SendToConnection(first.ID, []byte("stale")); err == nil {
		t.Fatal("send to disconnected connection succeeded")
	}
}

func TestTCPPacketMetadataIsLosslessAndCallbackSafe(t *testing.T) {
	var mu sync.Mutex
	var packets []Packet
	gotPacket := make(chan struct{}, 2)
	e := newTCPServerForTest(t, func(_ string, data []byte) {
		if len(data) > 0 {
			data[0] = 'X'
		}
	})
	e.SetOnPacket(func(packet Packet) {
		mu.Lock()
		packets = append(packets, packet)
		mu.Unlock()
		gotPacket <- struct{}{}
	})
	client := dialTestClients(t, e, 1)[0]
	info := waitConnections(t, e, 1)[0]
	if _, err := client.Write([]byte{0, 0xff, 'A'}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gotPacket:
	case <-time.After(time.Second):
		t.Fatal("packet callback timed out")
	}

	mu.Lock()
	packet := packets[0]
	mu.Unlock()
	if packet.ID == "" || packet.Timestamp.IsZero() || packet.Direction != "RX" || packet.Transport != "TCP" ||
		packet.ConnectionID != info.ID || packet.Endpoint != client.LocalAddr().String() || packet.Leg != "network" ||
		!bytes.Equal(packet.Data, []byte{0, 0xff, 'A'}) {
		t.Fatalf("packet metadata = %+v data=% X", packet, packet.Data)
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"id"`, `"timestamp"`, `"direction"`, `"transport"`, `"connection_id"`, `"endpoint"`, `"source"`, `"leg"`, `"data"`} {
		if !bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("packet JSON missing %s: %s", field, encoded)
		}
	}
}

func TestUDPExplicitTargetAndPeerMetadata(t *testing.T) {
	e, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Connect(Config{Mode: ModeUDPServer, Address: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	if err := e.SendToUDP(peer.LocalAddr().String(), []byte("explicit")); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _, err := peer.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "explicit" {
		t.Fatalf("UDP explicit send = %q, %v", buf[:n], err)
	}
	peers := e.UDPPeers()
	if len(peers) != 1 || peers[0].RemoteAddress != peer.LocalAddr().String() || peers[0].Transport != "UDP" || peers[0].TXCount != 1 {
		t.Fatalf("UDP peers = %+v", peers)
	}
}

func TestSetMaxConnectionsRejectsOnlyExcessClients(t *testing.T) {
	e := newTCPServerForTest(t, nil)
	e.SetMaxConnections(1)
	clients := dialTestClients(t, e, 2)
	infos := waitConnections(t, e, 1)
	if infos[0].RemoteAddress != clients[0].LocalAddr().String() {
		t.Fatalf("first client was not retained: %+v", infos)
	}
	if err := clients[1].SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := clients[1].Read(make([]byte, 1)); err == nil {
		t.Fatal("excess TCP client was not closed")
	}
}

func TestStorePacketMetadataAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartSession("TCP 服务端", "127.0.0.1:0", ""); err != nil {
		t.Fatal(err)
	}
	packet := Packet{ID: "packet-test", Timestamp: time.Now(), Direction: "RX", Transport: "TCP", ConnectionID: "tcp-test", Endpoint: "127.0.0.1:1234", Source: "TCP 127.0.0.1:1234", Leg: "network", Data: []byte{0, 0xff}}
	if err := store.Record(packet); err != nil {
		t.Fatal(err)
	}
	var id, direction, transport, connectionID, endpoint, leg string
	var raw []byte
	if err := store.db.QueryRow(`SELECT packet_id, direction, transport, connection_id, endpoint, leg, raw_data FROM received_data ORDER BY id DESC LIMIT 1`).Scan(&id, &direction, &transport, &connectionID, &endpoint, &leg, &raw); err != nil {
		t.Fatal(err)
	}
	if id != packet.ID || direction != "RX" || transport != "TCP" || connectionID != "tcp-test" || endpoint != packet.Endpoint || leg != "network" || !bytes.Equal(raw, packet.Data) {
		t.Fatalf("stored metadata = %q %q %q %q %q %q % X", id, direction, transport, connectionID, endpoint, leg, raw)
	}
	path := store.path
	store.Close()

	// Reopening an existing database proves the additive migration is idempotent.
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen migrated database %s: %v", path, err)
	}
	store.Close()
}

func TestBroadcastRecordsEachActualConnection(t *testing.T) {
	e := newTCPServerForTest(t, nil)
	clients := dialTestClients(t, e, 2)
	waitConnections(t, e, 2)
	var mu sync.Mutex
	var packets []Packet
	e.SetOnPacket(func(p Packet) { mu.Lock(); packets = append(packets, p); mu.Unlock() })
	if err := e.Send("broadcast", false, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		readExactly(t, c, "broadcast")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(packets) != 2 {
		t.Fatalf("want one physical TX per connection, got %+v", packets)
	}
	if packets[0].ConnectionID == "" || packets[0].ConnectionID == packets[1].ConnectionID {
		t.Fatalf("missing broadcast provenance: %+v", packets)
	}
}

func TestLegacySchemaMigrationPreservesRawData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial-data-"+time.Now().Format("20060102")+"-001.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions(id INTEGER PRIMARY KEY,started_at TEXT,ended_at TEXT,mode TEXT,endpoint TEXT,parameters TEXT);
 CREATE TABLE received_data(id INTEGER PRIMARY KEY,session_id INTEGER,received_at TEXT,source TEXT,size_bytes INTEGER,raw_data BLOB,text_data TEXT,is_utf8 INTEGER);
 INSERT INTO sessions VALUES(1,'2026-01-01',NULL,'TCP 客户端','old','');
 INSERT INTO received_data VALUES(1,1,'2026-01-01','TCP old',2,X'00FF','',0)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var raw []byte
	var id string
	if err := store.db.QueryRow("SELECT raw_data,packet_id FROM received_data WHERE id=1").Scan(&raw, &id); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, []byte{0, 255}) || id != "" {
		t.Fatalf("legacy record modified: % X %q", raw, id)
	}
}

func TestStaleReaderCannotEnterReplacementSession(t *testing.T) {
	e := newTCPServerForTest(t, nil)
	oldEpoch := e.epoch
	old := e.newPacket("RX", "TCP", "old", "old", "TCP old", "network", []byte("stale"))
	if err := e.Connect(Config{Mode: ModeTCPServer, Address: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	e.receivePacket(oldEpoch, old)
	var count int
	if err := e.store.db.QueryRow("SELECT COUNT(*) FROM received_data WHERE packet_id=?", old.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stale reader entered replacement session")
	}
}
