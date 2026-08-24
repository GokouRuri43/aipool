package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type AgentConfig struct {
	Role         string
	PairID       string
	RelayAddress string
	RelayToken   string
	TunnelKey    string
	TLSConfig    *tls.Config
	Dialer       func(context.Context, string) (net.Conn, error)
	Forwards     map[string]string // local listen address -> peer target name
	Targets      map[string]string // target name -> local service address
}

type Agent struct {
	cfg       AgentConfig
	listeners []net.Listener
	closeOnce sync.Once
	closed    chan struct{}
}

func NewAgent(cfg AgentConfig) (*Agent, error) {
	if peerRole(cfg.Role) == "" || cfg.PairID == "" || cfg.RelayAddress == "" || cfg.RelayToken == "" || cfg.TunnelKey == "" {
		return nil, fmt.Errorf("role, pair ID, relay address, relay token and tunnel key are required")
	}
	if !ValidPairToken(cfg.RelayToken) {
		return nil, fmt.Errorf("relay token must contain at least 32 base64url bytes")
	}
	if secret, err := base64.RawURLEncoding.DecodeString(cfg.TunnelKey); err != nil || len(secret) < 32 {
		return nil, fmt.Errorf("tunnel key must contain at least 32 base64url bytes")
	}
	if cfg.PairID != PairIDFromToken(cfg.RelayToken) {
		return nil, fmt.Errorf("pair ID does not match relay token")
	}
	for _, target := range cfg.Forwards {
		if target != "provider-host" && target != "provider-rpc" && target != "requester-control" {
			return nil, fmt.Errorf("unsupported tunnel target %q", target)
		}
	}
	for target := range cfg.Targets {
		if target != "provider-host" && target != "provider-rpc" && target != "requester-control" {
			return nil, fmt.Errorf("unsupported tunnel target %q", target)
		}
	}
	if cfg.Dialer == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		cfg.Dialer = func(ctx context.Context, address string) (net.Conn, error) {
			raw, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return nil, err
			}
			if cfg.TLSConfig == nil {
				return raw, nil
			}
			conn := tls.Client(raw, cfg.TLSConfig.Clone())
			if err := conn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return conn, nil
		}
	}
	return &Agent{cfg: cfg, closed: make(chan struct{})}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	for address, target := range a.cfg.Forwards {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			a.Close()
			return fmt.Errorf("listen %s: %w", address, err)
		}
		a.listeners = append(a.listeners, listener)
		go a.acceptLoop(listener, target)
	}
	backoff := time.Second
	for {
		if err := a.controlSession(ctx); err == nil {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			a.Close()
			return ctx.Err()
		case <-a.closed:
			return nil
		case <-time.After(backoff):
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		close(a.closed)
		for _, listener := range a.listeners {
			listener.Close()
		}
	})
	return nil
}

func (a *Agent) controlSession(ctx context.Context) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stopClose := make(chan struct{})
	defer close(stopClose)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-a.closed:
			conn.Close()
		case <-stopClose:
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	hello := handshake{Type: "control", Role: a.cfg.Role, PairID: a.cfg.PairID}
	reader := bufio.NewReader(conn)
	if err := a.authenticate(conn, reader, hello); err != nil {
		return err
	}
	var ready controlMessage
	if err := readJSONLine(reader, &ready); err != nil || ready.Type != "ready" {
		return fmt.Errorf("relay rejected control session")
	}
	_ = conn.SetDeadline(time.Time{})
	for {
		var message controlMessage
		if err := readJSONLine(reader, &message); err != nil {
			return err
		}
		if message.Type == "open" {
			go a.openTarget(ctx, message.StreamID, message.Target)
		}
	}
}

func (a *Agent) acceptLoop(listener net.Listener, target string) {
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go a.openForward(local, target)
	}
}

func (a *Agent) openForward(local net.Conn, target string) {
	defer local.Close()
	streamID, err := randomID()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var remote net.Conn
	for {
		remote, err = a.openStream(ctx, streamID, target)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-a.closed:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer remote.Close()
	bridge(local, remote)
}

func (a *Agent) openTarget(ctx context.Context, streamID, target string) {
	address, ok := a.cfg.Targets[target]
	if !ok {
		return
	}
	local, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return
	}
	defer local.Close()
	remote, err := a.openStream(ctx, streamID, target)
	if err != nil {
		return
	}
	defer remote.Close()
	bridge(local, remote)
}

func (a *Agent) openStream(ctx context.Context, streamID, target string) (net.Conn, error) {
	conn, err := a.dial(ctx)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	hello := handshake{Type: "stream", Role: a.cfg.Role, PairID: a.cfg.PairID, StreamID: streamID, Target: target}
	reader := bufio.NewReader(conn)
	if err := a.authenticate(conn, reader, hello); err != nil {
		conn.Close()
		return nil, err
	}
	var response controlMessage
	if err := readJSONLine(reader, &response); err != nil || response.Type != "ready" {
		conn.Close()
		return nil, fmt.Errorf("relay stream was not paired")
	}
	_ = conn.SetDeadline(time.Time{})
	return wrapSecure(&bufferedConn{Conn: conn, reader: reader}, a.cfg.TunnelKey, a.cfg.PairID, streamID, a.cfg.Role)
}

func (a *Agent) dial(ctx context.Context) (net.Conn, error) {
	return a.cfg.Dialer(ctx, a.cfg.RelayAddress)
}

func (a *Agent) authenticate(conn net.Conn, reader *bufio.Reader, hello handshake) error {
	if err := writeJSONLine(conn, hello); err != nil {
		return err
	}
	var challenge authChallenge
	if err := readJSONLine(reader, &challenge); err != nil || challenge.Type != "auth_challenge" || challenge.Nonce == "" {
		return fmt.Errorf("relay did not provide a valid authentication challenge")
	}
	return writeJSONLine(conn, authResponse{Type: "auth_response", Signature: signChallenge(a.cfg.RelayToken, hello, challenge.Nonce)})
}

func bridge(left, right net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(left, right); done <- struct{}{} }()
	go func() { _, _ = io.Copy(right, left); done <- struct{}{} }()
	<-done
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(data []byte) (int, error) { return c.reader.Read(data) }
