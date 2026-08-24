package tunnel

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RelayConfig struct {
	MaxConnections        int
	MaxConnectionsPerIP   int
	MaxConnectionsPerPair int
	MaxPendingPerPair     int
	HandshakeTimeout      time.Duration
	StreamTimeout         time.Duration
}

type Relay struct {
	mu                sync.Mutex
	controls          map[string]map[string]*controlPeer
	streams           map[string]*pendingStream
	connectionsByIP   map[string]int
	connectionsByPair map[string]int
	activeConnections int
	maxPendingPerPair int
	config            RelayConfig
	bytesRelayed      atomic.Uint64
}

type controlPeer struct {
	conn net.Conn
	mu   sync.Mutex
}
type pendingStream struct {
	requester net.Conn
	provider  net.Conn
	created   time.Time
	target    string
}

type countedConn struct {
	net.Conn
	closeOnce sync.Once
	onClose   func()
}

func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(c.onClose)
	return err
}

func NewRelay() *Relay {
	return NewRelayWithConfig(RelayConfig{})
}

func NewRelayWithConfig(config RelayConfig) *Relay {
	if config.MaxConnections <= 0 {
		config.MaxConnections = 4096
	}
	if config.MaxConnectionsPerIP <= 0 {
		config.MaxConnectionsPerIP = 256
	}
	if config.MaxConnectionsPerPair <= 0 {
		config.MaxConnectionsPerPair = 512
	}
	if config.MaxPendingPerPair <= 0 {
		config.MaxPendingPerPair = 128
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 15 * time.Second
	}
	if config.StreamTimeout <= 0 {
		config.StreamTimeout = 30 * time.Second
	}
	return &Relay{
		controls: map[string]map[string]*controlPeer{}, streams: map[string]*pendingStream{},
		connectionsByIP: map[string]int{}, connectionsByPair: map[string]int{},
		maxPendingPerPair: config.MaxPendingPerPair, config: config,
	}
}

func (r *Relay) Serve(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		ip := remoteIP(conn.RemoteAddr())
		if !r.acquireConnection(ip) {
			conn.Close()
			continue
		}
		go r.handle(&countedConn{Conn: conn, onClose: func() { r.releaseConnection(ip) }})
	}
}

func (r *Relay) handle(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(r.config.HandshakeTimeout))
	reader := bufio.NewReader(conn)
	var hello handshake
	if err := readJSONLine(reader, &hello); err != nil {
		conn.Close()
		return
	}
	if hello.PairID == "" || peerRole(hello.Role) == "" || !r.authenticate(conn, reader, hello) {
		_ = writeJSONLine(conn, controlMessage{Type: "error", Error: "unauthorized"})
		conn.Close()
		return
	}
	if !r.acquirePair(hello.PairID) {
		_ = writeJSONLine(conn, controlMessage{Type: "error", Error: "pair connection limit reached"})
		conn.Close()
		return
	}
	conn = &countedConn{Conn: conn, onClose: func() { r.releasePair(hello.PairID) }}
	_ = conn.SetDeadline(time.Time{})
	switch hello.Type {
	case "control":
		r.handleControl(conn, reader, hello)
	case "stream":
		r.handleStream(conn, hello)
	default:
		conn.Close()
	}
}

func (r *Relay) handleControl(conn net.Conn, reader *bufio.Reader, hello handshake) {
	peer := &controlPeer{conn: conn}
	r.mu.Lock()
	roles := r.controls[hello.PairID]
	if roles == nil {
		roles = map[string]*controlPeer{}
		r.controls[hello.PairID] = roles
	}
	old := roles[hello.Role]
	roles[hello.Role] = peer
	r.mu.Unlock()
	if old != nil {
		old.conn.Close()
	}
	if err := peer.write(controlMessage{Type: "ready"}); err != nil {
		conn.Close()
		return
	}
	defer func() {
		r.mu.Lock()
		if r.controls[hello.PairID][hello.Role] == peer {
			delete(r.controls[hello.PairID], hello.Role)
			if len(r.controls[hello.PairID]) == 0 {
				delete(r.controls, hello.PairID)
			}
		}
		r.mu.Unlock()
		conn.Close()
	}()
	for {
		var message controlMessage
		if err := readJSONLine(reader, &message); err != nil {
			return
		}
		if message.Type == "ping" {
			_ = peer.write(controlMessage{Type: "pong"})
		}
	}
}

func (r *Relay) handleStream(conn net.Conn, hello handshake) {
	if hello.StreamID == "" || (hello.Target != "provider-host" && hello.Target != "provider-rpc" && hello.Target != "requester-control") {
		conn.Close()
		return
	}
	key := hello.PairID + "|" + hello.StreamID
	r.mu.Lock()
	if r.pendingForPair(hello.PairID) >= r.maxPendingPerPair {
		r.mu.Unlock()
		conn.Close()
		return
	}
	pending := r.streams[key]
	if pending == nil {
		pending = &pendingStream{created: time.Now(), target: hello.Target}
		r.streams[key] = pending
		go r.expireStream(key, pending, r.config.StreamTimeout)
	} else if pending.target != hello.Target {
		r.mu.Unlock()
		conn.Close()
		return
	}
	if hello.Role == "requester" {
		if pending.requester != nil {
			r.mu.Unlock()
			conn.Close()
			return
		}
		pending.requester = conn
	} else {
		if pending.provider != nil {
			r.mu.Unlock()
			conn.Close()
			return
		}
		pending.provider = conn
	}
	paired := pending.requester != nil && pending.provider != nil
	peer := r.controls[hello.PairID][peerRole(hello.Role)]
	if paired {
		delete(r.streams, key)
	}
	r.mu.Unlock()
	if peer == nil && !paired {
		r.removeStream(key, conn)
		conn.Close()
		return
	}
	if !paired {
		if err := peer.write(controlMessage{Type: "open", StreamID: hello.StreamID, Target: hello.Target}); err != nil {
			r.removeStream(key, conn)
			conn.Close()
		}
		return
	}
	if err := writeJSONLine(pending.requester, controlMessage{Type: "ready"}); err != nil {
		pending.requester.Close()
		pending.provider.Close()
		return
	}
	if err := writeJSONLine(pending.provider, controlMessage{Type: "ready"}); err != nil {
		pending.requester.Close()
		pending.provider.Close()
		return
	}
	go r.relayPair(pending.requester, pending.provider)
}

func (r *Relay) pendingForPair(pairID string) int {
	prefix := pairID + "|"
	count := 0
	for key := range r.streams {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

func (r *Relay) expireStream(key string, expected *pendingStream, after time.Duration) {
	timer := time.NewTimer(after)
	defer timer.Stop()
	<-timer.C
	r.mu.Lock()
	if r.streams[key] == expected {
		delete(r.streams, key)
		if expected.requester != nil {
			expected.requester.Close()
		}
		if expected.provider != nil {
			expected.provider.Close()
		}
	}
	r.mu.Unlock()
}

func (r *Relay) removeStream(key string, conn net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pending := r.streams[key]; pending != nil && (pending.requester == conn || pending.provider == conn) {
		delete(r.streams, key)
	}
}

func (p *controlPeer) write(message controlMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return writeJSONLine(p.conn, message)
}

func (r *Relay) relayPair(left, right net.Conn) {
	defer left.Close()
	defer right.Close()
	done := make(chan struct{}, 2)
	go func() { n, _ := io.Copy(left, right); r.bytesRelayed.Add(uint64(n)); done <- struct{}{} }()
	go func() { n, _ := io.Copy(right, left); r.bytesRelayed.Add(uint64(n)); done <- struct{}{} }()
	<-done
}

func (r *Relay) Stats() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("pairs=%d active_connections=%d pending_streams=%d relayed_bytes=%d", len(r.controls), r.activeConnections, len(r.streams), r.bytesRelayed.Load())
}

func (r *Relay) authenticate(conn net.Conn, reader *bufio.Reader, hello handshake) bool {
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return false
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	if writeJSONLine(conn, authChallenge{Type: "auth_challenge", Nonce: nonce}) != nil {
		return false
	}
	var response authResponse
	return readJSONLine(reader, &response) == nil && response.Type == "auth_response" && verifyChallenge(hello, nonce, response.Signature)
}

func remoteIP(address net.Addr) string {
	if host, _, err := net.SplitHostPort(address.String()); err == nil {
		return host
	}
	return address.String()
}

func (r *Relay) acquireConnection(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeConnections >= r.config.MaxConnections || r.connectionsByIP[ip] >= r.config.MaxConnectionsPerIP {
		return false
	}
	r.activeConnections++
	r.connectionsByIP[ip]++
	return true
}

func (r *Relay) releaseConnection(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeConnections--
	r.connectionsByIP[ip]--
	if r.connectionsByIP[ip] == 0 {
		delete(r.connectionsByIP, ip)
	}
}

func (r *Relay) acquirePair(pairID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connectionsByPair[pairID] >= r.config.MaxConnectionsPerPair {
		return false
	}
	r.connectionsByPair[pairID]++
	return true
}

func (r *Relay) releasePair(pairID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectionsByPair[pairID]--
	if r.connectionsByPair[pairID] == 0 {
		delete(r.connectionsByPair, pairID)
	}
}
