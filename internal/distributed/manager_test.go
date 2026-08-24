package distributed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/local/aipool/internal/api"
)

func TestCreateGroupSendsModelLayoutAndAuthorization(t *testing.T) {
	want := api.DistributedGroupRequest{
		Model: "local-model", ModelDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ModelSize: 12345, ModelFormat: "gguf", LayerCount: 3, HiddenSize: 1536,
		LayerBytes: []int64{100, 200, 300}, MinNodes: 2, MaxNodes: 3,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/distributed/groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-AIPool-Client-Secret") != "client-secret" {
			t.Fatal("client authorization was not forwarded")
		}
		var got api.DistributedGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("group request mismatch:\n got %#v\nwant %#v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.DistributedGroup{GroupID: "group-1", Model: want.Model, Stages: []api.StageAssignment{
			{NodeID: "local", Endpoint: "127.0.0.1:50051", TensorBytes: 300},
			{NodeID: "friend", Endpoint: "127.0.0.1:50052", TensorBytes: 300},
		}})
	}))
	defer server.Close()

	manager := NewManager(ManagerConfig{ControlURL: server.URL + "/", ClientSecret: "client-secret"})
	group, err := manager.createGroup(t.Context(), want)
	if err != nil {
		t.Fatal(err)
	}
	if group.GroupID != "group-1" {
		t.Fatalf("unexpected group: %#v", group)
	}
}

func TestGroupNodesPreservesStageOrder(t *testing.T) {
	group := api.DistributedGroup{Stages: []api.StageAssignment{{NodeID: "local"}, {NodeID: "friend-1"}, {NodeID: "friend-2"}}}
	want := []string{"local", "friend-1", "friend-2"}
	if got := groupNodes(group); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
