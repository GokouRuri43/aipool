package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/control"
	"github.com/local/aipool/internal/modelcatalog"
)

func TestConfiguredEmptyLocalCatalogDoesNotExposeHostModels(t *testing.T) {
	catalog, err := modelcatalog.Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewWithConfig(Config{ControlURL: "http://127.0.0.1:1", ClientSecret: "secret", Catalog: catalog})
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"data":[]`) {
		t.Fatalf("unexpected local model response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestUnreachableSelectedHostReturnsBadGateway(t *testing.T) {
	const secret = "test-secret"
	controlServer := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlServer.Close()
	registerForProxyTest(t, controlServer.URL, secret, api.NodeRegistration{
		NodeID: "offline-host", Endpoint: "http://127.0.0.1:1", Models: []string{"model"},
		Available: true, Runtime: api.RuntimeStatus{Ready: true},
	})

	service := NewWithConfig(Config{
		ControlURL: controlServer.URL, ClientSecret: secret,
		ControlTimeout: time.Second, ConnectTimeout: 100 * time.Millisecond,
		ResponseHeaderTimeout: 100 * time.Millisecond, PreflightTimeout: 100 * time.Millisecond,
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`))
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "selected host is unreachable") {
		t.Fatalf("unexpected error: %s", recorder.Body.String())
	}
}

func TestControlTimeoutReturnsPromptly(t *testing.T) {
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hanging.Close()
	service := NewWithConfig(Config{ControlURL: hanging.URL, ClientSecret: "secret", ControlTimeout: 50 * time.Millisecond})

	started := time.Now()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", recorder.Code)
	}
	if time.Since(started) > time.Second {
		t.Fatal("control timeout did not terminate promptly")
	}
}

func TestParallelRequestsUseDifferentNodesAndExposeSelection(t *testing.T) {
	const secret = "parallel-secret"
	controlServer := httptest.NewServer(control.New(secret, time.Minute).Handler())
	defer controlServer.Close()
	release := make(chan struct{})
	started := make(chan string, 2)
	makeHost := func(nodeID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			started <- nodeID
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		}))
	}
	firstHost := makeHost("node-a")
	defer firstHost.Close()
	secondHost := makeHost("node-b")
	defer secondHost.Close()
	registerForProxyTest(t, controlServer.URL, secret, api.NodeRegistration{NodeID: "node-a", Endpoint: firstHost.URL, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, MaxConcurrency: 1})
	registerForProxyTest(t, controlServer.URL, secret, api.NodeRegistration{NodeID: "node-b", Endpoint: secondHost.URL, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, MaxConcurrency: 1})
	proxyServer := httptest.NewServer(New(controlServer.URL, secret).Handler())
	defer proxyServer.Close()
	type result struct {
		node string
		err  error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"parallel"}]}`))
			if err != nil {
				results <- result{err: err}
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			results <- result{node: resp.Header.Get("X-AIPool-Node-ID")}
		}()
	}
	used := map[string]bool{}
	for range 2 {
		select {
		case node := <-started:
			used[node] = true
		case <-time.After(3 * time.Second):
			t.Fatal("parallel requests did not reach both nodes")
		}
	}
	close(release)
	responseNodes := map[string]bool{}
	for range 2 {
		out := <-results
		if out.err != nil {
			t.Fatal(out.err)
		}
		responseNodes[out.node] = true
	}
	if len(used) != 2 || len(responseNodes) != 2 {
		t.Fatalf("requests were not spread: hosts=%v headers=%v", used, responseNodes)
	}
}

func registerForProxyTest(t *testing.T, baseURL, secret string, registration api.NodeRegistration) {
	t.Helper()
	body, _ := json.Marshal(registration)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/nodes/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Host-Secret", secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("registration failed: %s: %s", resp.Status, data)
	}
}
