package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestBidirectionalEncryptedReverseTunnel(t *testing.T) {
	tlsConfig, fingerprint := testCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	relay := NewRelay()
	go func() { _ = relay.Serve(listener) }()

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwardAddress := local.Addr().String()
	local.Close()

	pairToken := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	pairID := PairIDFromToken(pairToken)
	tunnelKey := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	clientTLS, err := PinnedTLSConfig("relay.test", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewAgent(AgentConfig{Role: "provider", PairID: pairID, RelayAddress: listener.Addr().String(), RelayToken: pairToken, TunnelKey: tunnelKey, TLSConfig: clientTLS, Targets: map[string]string{"provider-host": echo.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := NewAgent(AgentConfig{Role: "requester", PairID: pairID, RelayAddress: listener.Addr().String(), RelayToken: pairToken, TunnelKey: tunnelKey, TLSConfig: clientTLS, Forwards: map[string]string{forwardAddress: "provider-host"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer provider.Close()
	defer requester.Close()
	go provider.Run(ctx)
	go requester.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", forwardAddress, 200*time.Millisecond)
		if err == nil {
			message := []byte("encrypted model bytes")
			if _, err := conn.Write(message); err != nil {
				t.Fatal(err)
			}
			response := make([]byte, len(message))
			if _, err := io.ReadFull(conn, response); err != nil {
				t.Fatal(err)
			}
			conn.Close()
			if string(response) != string(message) {
				t.Fatalf("unexpected response %q", response)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel did not become ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAgentRejectsMismatchedPairToken(t *testing.T) {
	_, err := NewAgent(AgentConfig{Role: "provider", PairID: "wrong", RelayAddress: "relay", RelayToken: base64.RawURLEncoding.EncodeToString(bytesOf(t, 32)), TunnelKey: base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))})
	if err == nil {
		t.Fatal("expected mismatched pair token to be rejected")
	}
}

func TestAgentRejectsWeakSecrets(t *testing.T) {
	_, err := NewAgent(AgentConfig{Role: "provider", PairID: PairIDFromToken("weak"), RelayAddress: "relay", RelayToken: "weak", TunnelKey: "weak"})
	if err == nil {
		t.Fatal("expected weak pair and tunnel secrets to be rejected")
	}
}

func TestAgentAcceptsProviderRPCTarget(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	key := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	_, err := NewAgent(AgentConfig{Role: "provider", PairID: PairIDFromToken(token), RelayAddress: "relay", RelayToken: token, TunnelKey: key, Targets: map[string]string{"provider-rpc": "127.0.0.1:50052"}})
	if err != nil {
		t.Fatalf("provider RPC target was rejected: %v", err)
	}
}

func TestChallengeCannotBeSignedWithAnotherPairToken(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	otherToken := base64.RawURLEncoding.EncodeToString(bytesOf(t, 32))
	hello := handshake{Type: "control", Role: "requester", PairID: PairIDFromToken(token)}
	signature := signChallenge(otherToken, hello, "one-time-nonce")
	if verifyChallenge(hello, "one-time-nonce", signature) {
		t.Fatal("a signature made by another pair token was accepted")
	}
	valid := signChallenge(token, hello, "one-time-nonce")
	if !verifyChallenge(hello, "one-time-nonce", valid) {
		t.Fatal("valid challenge response was rejected")
	}
	if verifyChallenge(hello, "replayed-with-new-nonce", valid) {
		t.Fatal("challenge response could be replayed with another nonce")
	}
}

func TestRelayConnectionLimits(t *testing.T) {
	relay := NewRelayWithConfig(RelayConfig{MaxConnections: 2, MaxConnectionsPerIP: 1})
	if !relay.acquireConnection("192.0.2.1") {
		t.Fatal("first connection should be admitted")
	}
	if relay.acquireConnection("192.0.2.1") {
		t.Fatal("second connection from the same IP should be rejected")
	}
	if !relay.acquireConnection("192.0.2.2") {
		t.Fatal("connection from another IP should be admitted")
	}
	if relay.acquireConnection("192.0.2.3") {
		t.Fatal("global connection limit should be enforced")
	}
	relay.releaseConnection("192.0.2.1")
	if !relay.acquireConnection("192.0.2.3") {
		t.Fatal("released capacity should be reusable")
	}
}

func bytesOf(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func testCertificate(t *testing.T) (*tls.Config, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.test"}, DNSNames: []string{"relay.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(der)
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, hex.EncodeToString(digest[:])
}
