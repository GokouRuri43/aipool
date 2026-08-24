package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/modelcache"
)

func TestRuntimeHealthAndModelMapping(t *testing.T) {
	seenModel := ""
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			seenModel, _ = body["model"].(string)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runtime.Close()

	service := New(Config{NodeID: "node", Secret: "secret", Models: []string{"public-name"}, RuntimeURL: runtime.URL, RuntimeKind: "llama.cpp", RuntimeModel: "internal-name"})
	status := service.RuntimeStatus(context.Background())
	if !status.Ready || status.Kind != "llama.cpp" {
		t.Fatalf("unexpected runtime status: %#v", status)
	}

	token, _, err := issueTestLease("secret", "node", "public-name")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public-name","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if seenModel != "internal-name" {
		t.Fatalf("runtime received model %q", seenModel)
	}
}

func TestUnavailableRuntimeIsReported(t *testing.T) {
	service := New(Config{RuntimeURL: "http://127.0.0.1:1", RuntimeKind: "llama.cpp"})
	status := service.RuntimeStatus(context.Background())
	if status.Ready || status.Message == "" {
		t.Fatalf("unexpected runtime status: %#v", status)
	}
}

func TestModelUploadRequiresDigestBoundLease(t *testing.T) {
	store, err := modelcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(Config{NodeID: "node", LeaseSecret: "secret", ModelStore: store})
	token, _, err := issueTestLease("secret", "node", "model")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/model-cache/"+strings.Repeat("0", 64), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func testInventory() api.HardwareInventory { return api.HardwareInventory{} }
