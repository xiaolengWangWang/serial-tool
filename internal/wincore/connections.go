package wincore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Packet is one lossless socket/serial capture chunk or one explicit send.
// TCP reads are capture chunks and do not imply application message boundaries.
type Packet struct {
	ID           string `json:"id"`
	SessionKey   string `json:"session_key"`
	epoch        uint64
	Timestamp    time.Time `json:"timestamp"`
	Direction    string    `json:"direction"`
	Transport    string    `json:"transport"`
	ConnectionID string    `json:"connection_id"`
	Endpoint     string    `json:"endpoint"`
	Source       string    `json:"source"`
	Leg          string    `json:"leg"`
	Data         []byte    `json:"data"`
}

// ConnectionInfo is a point-in-time TCP connection or UDP peer snapshot.
type ConnectionInfo struct {
	ID            string    `json:"id"`
	Transport     string    `json:"transport"`
	LocalAddress  string    `json:"local_address"`
	RemoteAddress string    `json:"remote_address"`
	ConnectedAt   time.Time `json:"connected_at"`
	RXBytes       uint64    `json:"rx_bytes"`
	TXBytes       uint64    `json:"tx_bytes"`
	RXCount       uint64    `json:"rx_count"`
	TXCount       uint64    `json:"tx_count"`
	Active        bool      `json:"active"`
}

type trackedConnection struct {
	id          string
	conn        net.Conn
	epoch       uint64
	connectedAt time.Time
	writeMu     sync.Mutex
	rxBytes     uint64
	txBytes     uint64
	rxCount     uint64
	txCount     uint64
	manualClose uint32
}

func (c *trackedConnection) snapshot() ConnectionInfo {
	return ConnectionInfo{
		ID:            c.id,
		Transport:     "TCP",
		LocalAddress:  c.conn.LocalAddr().String(),
		RemoteAddress: c.conn.RemoteAddr().String(),
		ConnectedAt:   c.connectedAt,
		RXBytes:       atomic.LoadUint64(&c.rxBytes),
		TXBytes:       atomic.LoadUint64(&c.txBytes),
		RXCount:       atomic.LoadUint64(&c.rxCount),
		TXCount:       atomic.LoadUint64(&c.txCount),
		Active:        true,
	}
}

type udpPeerState struct {
	id          string
	address     string
	local       string
	connectedAt time.Time
	rxBytes     uint64
	txBytes     uint64
	rxCount     uint64
	txCount     uint64
}

func (p *udpPeerState) snapshot() ConnectionInfo {
	return ConnectionInfo{
		ID:            p.id,
		Transport:     "UDP",
		LocalAddress:  p.local,
		RemoteAddress: p.address,
		ConnectedAt:   p.connectedAt,
		RXBytes:       atomic.LoadUint64(&p.rxBytes),
		TXBytes:       atomic.LoadUint64(&p.txBytes),
		RXCount:       atomic.LoadUint64(&p.rxCount),
		TXCount:       atomic.LoadUint64(&p.txCount),
		Active:        true,
	}
}

func randomIDPrefix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func (e *Engine) nextID(kind string) string {
	return fmt.Sprintf("%s-%s-%d", kind, e.idPrefix, atomic.AddUint64(&e.idSeq, 1))
}

func (e *Engine) newPacket(direction, transport, connectionID, endpoint, source, leg string, data []byte) Packet {
	e.Lock()
	epoch := e.epoch
	e.Unlock()
	return Packet{
		epoch:        epoch,
		SessionKey:   fmt.Sprintf("%s-%d", e.idPrefix, epoch),
		ID:           e.nextID("packet"),
		Timestamp:    time.Now(),
		Direction:    direction,
		Transport:    transport,
		ConnectionID: connectionID,
		Endpoint:     endpoint,
		Source:       source,
		Leg:          leg,
		Data:         append([]byte(nil), data...),
	}
}

func (e *Engine) addTCPConnection(conn net.Conn, epoch uint64) *trackedConnection {
	c := &trackedConnection{id: e.nextID("tcp"), conn: conn, epoch: epoch, connectedAt: time.Now()}
	e.clients[conn] = c
	return c
}

func (e *Engine) isActiveConnection(c *trackedConnection) bool {
	e.Lock()
	active := e.epoch == c.epoch && e.clients != nil && e.clients[c.conn] == c
	e.Unlock()
	return active
}

// SetOnPacket registers the structured packet callback. Each callback owns its Data snapshot.
func (e *Engine) SetOnPacket(fn func(Packet)) {
	e.Lock()
	e.onPacket = fn
	e.Unlock()
}

// Connections returns active TCP connections sorted by connection time.
func (e *Engine) Connections() []ConnectionInfo {
	e.Lock()
	connections := make([]*trackedConnection, 0, len(e.clients))
	for _, c := range e.clients {
		connections = append(connections, c)
	}
	e.Unlock()
	sort.Slice(connections, func(i, j int) bool { return connections[i].connectedAt.Before(connections[j].connectedAt) })
	out := make([]ConnectionInfo, len(connections))
	for i, c := range connections {
		out[i] = c.snapshot()
	}
	return out
}

func (e *Engine) connectionByID(id string) *trackedConnection {
	e.Lock()
	defer e.Unlock()
	for _, c := range e.clients {
		if c.id == id && c.epoch == e.epoch {
			return c
		}
	}
	return nil
}

// SendToConnection sends one complete write to one current TCP connection.
func (e *Engine) SendToConnection(id string, data []byte) error {
	c := e.connectionByID(id)
	if c == nil {
		return fmt.Errorf("TCP Connection %q 不存在", id)
	}
	if err := e.writeTCP(c, data); err != nil {
		return err
	}
	e.storeSentPacket(e.newPacket("TX", "TCP", c.id, c.conn.RemoteAddr().String(), "发送", "network", data))
	return nil
}

// DisconnectConnection closes only the selected current TCP connection.
func (e *Engine) DisconnectConnection(id string) error {
	c := e.connectionByID(id)
	if c == nil {
		return fmt.Errorf("TCP Connection %q 不存在", id)
	}
	atomic.StoreUint32(&c.manualClose, 1)
	return c.conn.Close()
}

// UDPPeers returns peers observed or explicitly targeted in the current session.
func (e *Engine) UDPPeers() []ConnectionInfo {
	e.Lock()
	peers := make([]*udpPeerState, 0, len(e.udpPeers))
	for _, peer := range e.udpPeers {
		peers = append(peers, peer)
	}
	e.Unlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i].connectedAt.Before(peers[j].connectedAt) })
	out := make([]ConnectionInfo, len(peers))
	for i, peer := range peers {
		out[i] = peer.snapshot()
	}
	return out
}

func (e *Engine) udpPeerFor(address string, local string) *udpPeerState {
	e.Lock()
	defer e.Unlock()
	if e.udpPeers == nil {
		e.udpPeers = make(map[string]*udpPeerState)
	}
	peer := e.udpPeers[address]
	if peer == nil {
		peer = &udpPeerState{id: e.nextID("udp"), address: address, local: local, connectedAt: time.Now()}
		e.udpPeers[address] = peer
	}
	return peer
}

// SendToUDP sends one datagram to address without changing the default reply target.
func (e *Engine) SendToUDP(address string, data []byte) error {
	peer, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}
	e.Lock()
	conn, epoch := e.udp, e.epoch
	e.Unlock()
	if conn == nil {
		return errors.New("UDP 未连接")
	}
	if err := e.writeUDP(conn, epoch, peer, data); err != nil {
		return err
	}
	state := e.udpPeerFor(peer.String(), conn.LocalAddr().String())
	e.storeSentPacket(e.newPacket("TX", "UDP", state.id, peer.String(), "发送", "network", data))
	return nil
}

// SetBridgeReplyLatest selects serial replies to the most recent requesting TCP connection.
func (e *Engine) SetBridgeReplyLatest(enabled bool) {
	e.Lock()
	e.bridgeReplyLatest = enabled
	e.Unlock()
}

// SetMaxConnections limits subsequently accepted TCP server connections; zero is unlimited.
func (e *Engine) SetMaxConnections(max int) {
	if max < 0 {
		max = 0
	}
	e.Lock()
	e.maxConnections = max
	e.Unlock()
}
