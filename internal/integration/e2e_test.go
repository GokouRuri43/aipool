package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/control"
	"github.com/local/aipool/internal/host"
	"github.com/local/aipool/internal/modelcache"
	"github.com/local/aipool/internal/modelcatalog"
	"github.com/local/aipool/internal/proxy"
)

func TestEndToEndChatAndStreaming(t *testing.T) {
	const secret = "test-secret"
	controlServer := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlServer.Close()

	hostService := host.New(host.Config{NodeID: "test-gpu", Secret: secret, Models: []string{"mock-llm"}, ControlURL: controlServer.URL})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()
	hostService = host.New(host.Config{NodeID: "test-gpu", Endpoint: hostServer.URL, Secret: secret, Models: []string{"mock-llm"}, ControlURL: controlServer.URL})
	if err := hostService.Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	proxyServer := httptest.NewServer(proxy.New(controlServer.URL, secret).Handler())
	defer proxyServer.Close()

	plain := post(t, proxyServer.URL, `{"model":"mock-llm","messages":[{"role":"user","content":"hello"}]}`)
	defer plain.Body.Close()
	data, _ := io.ReadAll(plain.Body)
	if plain.StatusCode != http.StatusOK || !bytes.Contains(data, []byte("hello")) {
		t.Fatalf("unexpected response %s: %s", plain.Status, data)
	}

	stream := post(t, proxyServer.URL, `{"model":"mock-llm","stream":true,"messages":[{"role":"user","content":"stream hello"}]}`)
	defer stream.Body.Close()
	if got := stream.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("unexpected content type: %s", got)
	}
	scanner := bufio.NewScanner(stream.Body)
	foundDone := false
	for scanner.Scan() {
		if scanner.Text() == "data: [DONE]" {
			foundDone = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundDone {
		t.Fatal("stream did not terminate with [DONE]")
	}
}

func TestEndToEndWithSeparatedSecrets(t *testing.T) {
	const hostSecret = "host-registration-secret"
	const clientSecret = "requester-secret"
	const leaseSecret = "lease-signing-secret"
	controlServer := httptest.NewServer(control.NewWithSecrets(hostSecret, clientSecret, leaseSecret, time.Minute).Handler())
	defer controlServer.Close()

	hostService := host.New(host.Config{
		NodeID: "separated-secrets-node", Models: []string{"mock-llm"}, ControlURL: controlServer.URL,
		RegistrationSecret: hostSecret, LeaseSecret: leaseSecret,
	})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()
	hostService = host.New(host.Config{
		NodeID: "separated-secrets-node", Endpoint: hostServer.URL, Models: []string{"mock-llm"}, ControlURL: controlServer.URL,
		RegistrationSecret: hostSecret, LeaseSecret: leaseSecret,
	})
	if err := hostService.Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	proxyServer := httptest.NewServer(proxy.New(controlServer.URL, clientSecret).Handler())
	defer proxyServer.Close()
	resp := post(t, proxyServer.URL, `{"model":"mock-llm","messages":[{"role":"user","content":"separate secrets"}]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response %s: %s", resp.Status, data)
	}
}

func TestLocalModelIsUploadedThenInferredRemotely(t *testing.T) {
	const hostSecret = "host-secret"
	const clientSecret = "client-secret"
	const leaseSecret = "lease-secret"
	modelData := []byte("small fake GGUF model for transfer test")
	modelPath := filepath.Join(t.TempDir(), "local.gguf")
	if err := os.WriteFile(modelPath, modelData, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := modelcatalog.Load("", "user-model="+modelPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Lookup("user-model")

	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "user-model" {
			t.Errorf("runtime model = %v", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"remote upload worked"}}]}`)
	}))
	defer runtimeServer.Close()
	cache, err := modelcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeManagedRuntime{URL: runtimeServer.URL}

	controlServer := httptest.NewServer(control.NewWithSecrets(hostSecret, clientSecret, leaseSecret, time.Minute).Handler())
	defer controlServer.Close()
	hostService := host.New(host.Config{
		NodeID: "upload-node", ControlURL: controlServer.URL, RegistrationSecret: hostSecret, LeaseSecret: leaseSecret,
		ModelStore: cache, RuntimeManager: manager, MaxUploadChunk: 8,
	})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()
	hostService = host.New(host.Config{
		NodeID: "upload-node", Endpoint: hostServer.URL, ControlURL: controlServer.URL,
		RegistrationSecret: hostSecret, LeaseSecret: leaseSecret, ModelStore: cache, RuntimeManager: manager, MaxUploadChunk: 8,
	})
	if err := hostService.Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	proxyServer := httptest.NewServer(proxy.NewWithConfig(proxy.Config{
		ControlURL: controlServer.URL, ClientSecret: clientSecret, Catalog: catalog, UploadChunkSize: 8,
	}).Handler())
	defer proxyServer.Close()
	models, err := http.Get(proxyServer.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	modelList, _ := io.ReadAll(models.Body)
	models.Body.Close()
	if !bytes.Contains(modelList, []byte("user-model")) || bytes.Contains(modelList, []byte("upload-node")) {
		t.Fatalf("model discovery is not local: %s", modelList)
	}

	resp := post(t, proxyServer.URL, `{"model":"user-model","messages":[{"role":"user","content":"hello"}]}`)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(data, []byte("remote upload worked")) {
		t.Fatalf("unexpected response %s: %s", resp.Status, data)
	}
	status, err := cache.Status(entry.Digest, int64(len(modelData)))
	if err != nil || !status.Ready || manager.Digest != entry.Digest || manager.Path != cache.Path(entry.Digest) {
		t.Fatalf("model was not cached and loaded correctly: status=%#v manager=%#v err=%v", status, manager, err)
	}
}

type fakeManagedRuntime struct{ URL, Digest, Path string }

func (f *fakeManagedRuntime) Available() error { return nil }
func (f *fakeManagedRuntime) Ensure(_ context.Context, _ string, digest, path string) (string, error) {
	f.Digest, f.Path = digest, path
	return f.URL, nil
}

func TestHostRejectsMissingLease(t *testing.T) {
	hostServer := httptest.NewServer(host.New(host.Config{NodeID: "node", Secret: "secret", Models: []string{"model"}}).Handler())
	defer hostServer.Close()
	resp := post(t, hostServer.URL, `{"model":"model","messages":[{"role":"user","content":"hello"}]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %s", resp.Status)
	}
}

func TestLeaseCannotAccessAnotherModel(t *testing.T) {
	const secret = "test-secret"
	controlServer := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlServer.Close()
	hostService := host.New(host.Config{NodeID: "node", Secret: secret, Models: []string{"model-a", "model-b"}, ControlURL: controlServer.URL})
	hostServer := httptest.NewServer(hostService.Handler())
	defer hostServer.Close()
	hostService = host.New(host.Config{NodeID: "node", Endpoint: hostServer.URL, Secret: secret, Models: []string{"model-a", "model-b"}, ControlURL: controlServer.URL})
	if err := hostService.Register(context.Background()); err != nil {
		t.Fatal(err)
	}

	leaseRequest, _ := http.NewRequest(http.MethodPost, controlServer.URL+"/v1/leases", strings.NewReader(`{"model":"model-a"}`))
	leaseRequest.Header.Set("Content-Type", "application/json")
	leaseRequest.Header.Set("X-AIPool-Client-Secret", secret)
	leaseResponse, err := http.DefaultClient.Do(leaseRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseResponse.Body.Close()
	var lease struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(leaseResponse.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, hostServer.URL+"/v1/chat/completions", strings.NewReader(`{"model":"model-b","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+lease.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %s", resp.Status)
	}
}

func TestControlRejectsUntrustedHost(t *testing.T) {
	controlServer := httptest.NewServer(control.New("expected-secret", time.Minute).Handler())
	defer controlServer.Close()
	untrusted := host.New(host.Config{NodeID: "bad-node", Endpoint: "http://example.invalid", Secret: "wrong-secret", Models: []string{"model"}, ControlURL: controlServer.URL})
	if err := untrusted.Register(context.Background()); err == nil {
		t.Fatal("expected untrusted registration to fail")
	}
}

func TestSchedulerHonorsMinimumVRAM(t *testing.T) {
	const secret = "test-secret"
	controlServer := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlServer.Close()
	inventory := func() api.HardwareInventory {
		return api.HardwareInventory{GPUDevices: []api.GPUDevice{{Name: "test-gpu", MemoryFreeMB: 4096}}}
	}
	hostService := host.New(host.Config{NodeID: "node", Endpoint: "http://host.invalid", Secret: secret, Models: []string{"model"}, ControlURL: controlServer.URL, Inventory: inventory})
	if err := hostService.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, controlServer.URL+"/v1/leases", strings.NewReader(`{"model":"model","min_vram_mb":8192}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %s", resp.Status)
	}
}

func post(t *testing.T, baseURL, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
