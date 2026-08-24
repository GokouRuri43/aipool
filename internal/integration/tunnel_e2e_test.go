package integration

import (
	"bytes"
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/aipool/internal/control"
	"github.com/local/aipool/internal/host"
	"github.com/local/aipool/internal/modelcache"
	"github.com/local/aipool/internal/modelcatalog"
	"github.com/local/aipool/internal/proxy"
	"github.com/local/aipool/internal/tunnel"
)

func TestEndToEndChatThroughSelfHostedRelay(t *testing.T) {
	serverTLS, fingerprint := integrationCertificate(t)
	relayListener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer relayListener.Close()
	go func() { _ = tunnel.NewRelay().Serve(relayListener) }()
	clientTLS, err := tunnel.PinnedTLSConfig("relay.test", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	pairToken := base64.RawURLEncoding.EncodeToString(integrationRandom(t, 32))
	pairID := tunnel.PairIDFromToken(pairToken)
	tunnelKey := base64.RawURLEncoding.EncodeToString(integrationRandom(t, 32))
	const secret = "tunnel-test-secret"

	controlService := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlService.Close()
	hostService := host.New(host.Config{NodeID: "remote-provider", Endpoint: "http://127.0.0.1:39091", Secret: secret, Models: []string{"mock-llm"}, ControlURL: "http://127.0.0.1:39080"})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()

	requesterControlAddress := unusedAddress(t)
	requesterHostAddress := unusedAddress(t)
	providerControlAddress := unusedAddress(t)
	provider, err := tunnel.NewAgent(tunnel.AgentConfig{Role: "provider", PairID: pairID, RelayAddress: relayListener.Addr().String(), RelayToken: pairToken, TunnelKey: tunnelKey, TLSConfig: clientTLS, Forwards: map[string]string{providerControlAddress: "requester-control"}, Targets: map[string]string{"provider-host": hostServer.Listener.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := tunnel.NewAgent(tunnel.AgentConfig{Role: "requester", PairID: pairID, RelayAddress: relayListener.Addr().String(), RelayToken: pairToken, TunnelKey: tunnelKey, TLSConfig: clientTLS, Forwards: map[string]string{requesterHostAddress: "provider-host"}, Targets: map[string]string{"requester-control": controlService.Listener.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer provider.Close()
	defer requester.Close()
	go provider.Run(ctx)
	go requester.Run(ctx)

	// Register through Provider -> Relay -> Requester Control, while advertising
	// the Requester's local Host tunnel address to its local Proxy.
	tunneledHost := host.New(host.Config{NodeID: "remote-provider", Endpoint: "http://" + requesterHostAddress, Secret: secret, Models: []string{"mock-llm"}, ControlURL: "http://" + providerControlAddress})
	deadline := time.Now().Add(10 * time.Second)
	for {
		regCtx, regCancel := context.WithTimeout(ctx, time.Second)
		err = tunneledHost.Register(regCtx)
		regCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registration through tunnel failed: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	proxyServer := httptest.NewServer(proxy.New(controlService.URL, secret).Handler())
	defer proxyServer.Close()
	resp := post(t, proxyServer.URL, `{"model":"mock-llm","messages":[{"role":"user","content":"relay hello"}]}`)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !contains(data, []byte("relay hello")) {
		t.Fatalf("unexpected tunneled response %s: %s", resp.Status, data)
	}
	_ = requesterControlAddress
}

func TestLocalModelUploadAndInferenceThroughSelfHostedRelay(t *testing.T) {
	const hostSecret = "relay-upload-host-secret"
	const clientSecret = "relay-upload-client-secret"
	const leaseSecret = "relay-upload-lease-secret"

	modelData := bytes.Repeat([]byte("fake GGUF model block for encrypted relay transfer\n"), 32)
	modelPath := filepath.Join(t.TempDir(), "local.gguf")
	if err := os.WriteFile(modelPath, modelData, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := modelcatalog.Load("", "relay-model="+modelPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Lookup("relay-model")

	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"encrypted relay upload worked"}}]}`)
	}))
	defer runtimeServer.Close()
	cache, err := modelcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeManagedRuntime{URL: runtimeServer.URL}

	serverTLS, fingerprint := integrationCertificate(t)
	relayListener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer relayListener.Close()
	relay := tunnel.NewRelay()
	go func() { _ = relay.Serve(relayListener) }()
	clientTLS, err := tunnel.PinnedTLSConfig("relay.test", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	pairToken := base64.RawURLEncoding.EncodeToString(integrationRandom(t, 32))
	pairID := tunnel.PairIDFromToken(pairToken)
	tunnelKey := base64.RawURLEncoding.EncodeToString(integrationRandom(t, 32))

	controlService := httptest.NewServer(control.NewWithSecrets(hostSecret, clientSecret, leaseSecret, time.Minute).Handler())
	defer controlService.Close()
	hostService := host.New(host.Config{
		NodeID: "relay-upload-provider", RegistrationSecret: hostSecret, LeaseSecret: leaseSecret,
		ModelStore: cache, RuntimeManager: manager, MaxUploadChunk: 64,
	})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()

	requesterHostAddress := unusedAddress(t)
	providerControlAddress := unusedAddress(t)
	provider, err := tunnel.NewAgent(tunnel.AgentConfig{
		Role: "provider", PairID: pairID, RelayAddress: relayListener.Addr().String(), RelayToken: pairToken,
		TunnelKey: tunnelKey, TLSConfig: clientTLS, Forwards: map[string]string{providerControlAddress: "requester-control"},
		Targets: map[string]string{"provider-host": hostServer.Listener.Addr().String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := tunnel.NewAgent(tunnel.AgentConfig{
		Role: "requester", PairID: pairID, RelayAddress: relayListener.Addr().String(), RelayToken: pairToken,
		TunnelKey: tunnelKey, TLSConfig: clientTLS, Forwards: map[string]string{requesterHostAddress: "provider-host"},
		Targets: map[string]string{"requester-control": controlService.Listener.Addr().String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer provider.Close()
	defer requester.Close()
	go provider.Run(ctx)
	go requester.Run(ctx)

	registration := host.New(host.Config{
		NodeID: "relay-upload-provider", Endpoint: "http://" + requesterHostAddress,
		ControlURL: "http://" + providerControlAddress, RegistrationSecret: hostSecret, LeaseSecret: leaseSecret,
		ModelStore: cache, RuntimeManager: manager,
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		regCtx, regCancel := context.WithTimeout(ctx, time.Second)
		err = registration.Register(regCtx)
		regCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registration through relay failed: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	proxyServer := httptest.NewServer(proxy.NewWithConfig(proxy.Config{
		ControlURL: controlService.URL, ClientSecret: clientSecret, Catalog: catalog, UploadChunkSize: 64,
	}).Handler())
	defer proxyServer.Close()
	resp := post(t, proxyServer.URL, `{"model":"relay-model","messages":[{"role":"user","content":"upload through relay"}]}`)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(data, []byte("encrypted relay upload worked")) {
		t.Fatalf("unexpected relay upload response %s: %s", resp.Status, data)
	}
	status, err := cache.Status(entry.Digest, int64(len(modelData)))
	if err != nil || !status.Ready || manager.Digest != entry.Digest || manager.Path != cache.Path(entry.Digest) {
		t.Fatalf("model did not traverse relay correctly: status=%#v manager=%#v err=%v", status, manager, err)
	}
	if !strings.Contains(relay.Stats(), "relayed_bytes=") {
		t.Fatalf("relay statistics did not record traffic: %s", relay.Stats())
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}
func integrationRandom(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
func contains(data, part []byte) bool { return stringIndex(string(data), string(part)) >= 0 }
func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func integrationCertificate(t *testing.T) (*tls.Config, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "relay.test"}, DNSNames: []string{"relay.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	sum := sha256.Sum256(der)
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, hex.EncodeToString(sum[:])
}
