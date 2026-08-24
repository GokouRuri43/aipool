package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/auth"
)

func TestSchedulerPrefersMoreFreeVRAM(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "small", Endpoint: "http://small", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(4096)})
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "large", Endpoint: "http://large", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(8192)})

	body, _ := json.Marshal(api.LeaseRequest{Model: "model"})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/leases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lease api.Lease
	if err := json.NewDecoder(resp.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	if lease.NodeID != "large" {
		t.Fatalf("expected large node, got %#v", lease)
	}
}

func TestConcurrentLeasesSpreadAcrossNodesAndRelease(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "local", Scope: "local", MaxConcurrency: 1, Endpoint: "http://local", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(4096)})
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "friend-1", Scope: "remote", MaxConcurrency: 1, Endpoint: "http://friend", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(8192)})

	first := requestLease(t, server.URL, api.LeaseRequest{Model: "model"})
	second := requestLease(t, server.URL, api.LeaseRequest{Model: "model"})
	if first.NodeID == second.NodeID || first.LeaseID == "" || second.LeaseID == "" {
		t.Fatalf("parallel capacity was not spread: first=%#v second=%#v", first, second)
	}
	body, _ := json.Marshal(api.LeaseRequest{Model: "model"})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/leases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected exhausted pool, got %s", resp.Status)
	}

	release, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/leases/"+first.LeaseID+"/release", nil)
	release.Header.Set("X-AIPool-Client-Secret", "secret")
	releaseResp, err := http.DefaultClient.Do(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseResp.Body.Close()
	third := requestLease(t, server.URL, api.LeaseRequest{Model: "model"})
	if third.NodeID != first.NodeID {
		t.Fatalf("released node was not reused: %#v", third)
	}
}

func TestSchedulerSupportsNodeAndScopeSelection(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "mine", Scope: "local", MaxConcurrency: 1, Endpoint: "http://mine", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}})
	registerNode(t, server.URL, api.NodeRegistration{NodeID: "friend", Scope: "remote", MaxConcurrency: 1, Endpoint: "http://friend", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}})
	if lease := requestLease(t, server.URL, api.LeaseRequest{Model: "model", Scope: "local"}); lease.NodeID != "mine" {
		t.Fatalf("local scope selected %#v", lease)
	}
	if lease := requestLease(t, server.URL, api.LeaseRequest{Model: "model", NodeID: "friend"}); lease.NodeID != "friend" {
		t.Fatalf("node selection returned %#v", lease)
	}
}

func TestPerNodeCredentials(t *testing.T) {
	credentials := map[string]NodeCredential{"friend": {RegistrationSecret: "friend-host", LeaseSecret: "friend-lease"}}
	server := httptest.NewServer(NewWithNodeCredentials("fallback", "client", "fallback-lease", time.Minute, credentials).Handler())
	defer server.Close()
	body, _ := json.Marshal(api.NodeRegistration{NodeID: "friend", Endpoint: "http://friend", Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/nodes/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Host-Secret", "friend-host")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("per-node registration failed: %s", resp.Status)
	}
	lease := requestLeaseWithSecret(t, server.URL, api.LeaseRequest{Model: "model"}, "client")
	claims, err := auth.Verify([]byte("friend-lease"), lease.Token)
	if err != nil || claims.NodeID != "friend" {
		t.Fatalf("lease did not use node credential: %#v %v", claims, err)
	}
}

func requestLease(t *testing.T, baseURL string, request api.LeaseRequest) api.Lease {
	return requestLeaseWithSecret(t, baseURL, request, "secret")
}

func requestLeaseWithSecret(t *testing.T, baseURL string, request api.LeaseRequest, secret string) api.Lease {
	t.Helper()
	body, _ := json.Marshal(request)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/leases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease failed: %s", resp.Status)
	}
	var lease api.Lease
	if err := json.NewDecoder(resp.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestRequesterEndpointsRequireClientSecret(t *testing.T) {
	server := httptest.NewServer(NewWithSecrets("host", "client", "lease", time.Minute).Handler())
	defer server.Close()
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/nodes", ""},
		{http.MethodPost, "/v1/leases", `{"model":"model"}`},
	} {
		req, _ := http.NewRequest(test.method, server.URL+test.path, bytes.NewBufferString(test.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %s", test.method, test.path, resp.Status)
		}
	}
}

func TestNodeListingHidesRuntimeURL(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	registerNode(t, server.URL, api.NodeRegistration{
		NodeID: "node", Endpoint: "http://host", Models: []string{"model"}, Available: true,
		Runtime: api.RuntimeStatus{Ready: true, URL: "http://127.0.0.1:8081"},
	})
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/nodes", nil)
	req.Header.Set("X-AIPool-Client-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Data []api.Node `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].Runtime.URL != "" {
		t.Fatalf("runtime URL leaked: %#v", payload.Data)
	}
}

func registerNode(t *testing.T, baseURL string, registration api.NodeRegistration) {
	t.Helper()
	body, _ := json.Marshal(registration)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/nodes/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Host-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registration failed: %s", resp.Status)
	}
}

func inventoryWithFreeVRAM(free int) api.HardwareInventory {
	return api.HardwareInventory{GPUDevices: []api.GPUDevice{{MemoryFreeMB: free}}}
}

func TestDistributedGroupAtomicallyReservesContiguousStages(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	for _, node := range []api.NodeRegistration{
		{NodeID: "stage-a", Endpoint: "http://a", StageEndpoint: "tcp://a:18100", DistributedReady: true, MaxConcurrency: 1, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(12000)},
		{NodeID: "stage-b", Endpoint: "http://b", StageEndpoint: "tcp://b:18100", DistributedReady: true, MaxConcurrency: 1, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(8000)},
		{NodeID: "ordinary", Endpoint: "http://c", MaxConcurrency: 1, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(24000)},
	} {
		registerNode(t, server.URL, node)
	}
	request := api.DistributedGroupRequest{Model: "model", LayerCount: 40, HiddenSize: 4096, MinNodes: 2, MaxNodes: 2}
	create := func() (*http.Response, api.DistributedGroup) {
		body, _ := json.Marshal(request)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/distributed/groups", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-AIPool-Client-Secret", "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var group api.DistributedGroup
		if resp.StatusCode == http.StatusCreated {
			if err := json.NewDecoder(resp.Body).Decode(&group); err != nil {
				t.Fatal(err)
			}
		}
		resp.Body.Close()
		return resp, group
	}
	resp, group := create()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("group creation failed: %s", resp.Status)
	}
	if group.GroupID == "" || len(group.Stages) != 2 || group.Stages[0].LayerStart != 0 || group.Stages[0].LayerEnd != group.Stages[1].LayerStart || group.Stages[1].LayerEnd != 40 {
		t.Fatalf("invalid group: %#v", group)
	}
	for _, assigned := range group.Stages {
		claims, err := auth.Verify([]byte("secret"), assigned.Token)
		if err != nil || claims.GroupID != group.GroupID || claims.LayerStart != assigned.LayerStart || claims.LayerEnd != assigned.LayerEnd {
			t.Fatalf("stage token mismatch: %#v %v", claims, err)
		}
	}
	second, _ := create()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("atomic capacity was not held: %s", second.Status)
	}
	release, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/distributed/groups/"+group.GroupID+"/release", nil)
	release.Header.Set("X-AIPool-Client-Secret", "secret")
	releaseResp, err := http.DefaultClient.Do(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseResp.Body.Close()
	third, _ := create()
	if third.StatusCode != http.StatusCreated {
		t.Fatalf("released capacity was not reusable: %s", third.Status)
	}
}

func TestDistributedGroupRenewKeepsReservations(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	for _, id := range []string{"a", "b"} {
		registerNode(t, server.URL, api.NodeRegistration{NodeID: id, Endpoint: "http://" + id, StageEndpoint: "tcp://" + id + ":1", DistributedReady: true, MaxConcurrency: 1, Models: []string{"model"}, Available: true, Runtime: api.RuntimeStatus{Ready: true}, Hardware: inventoryWithFreeVRAM(2048)})
	}
	body, _ := json.Marshal(api.DistributedGroupRequest{Model: "model", LayerCount: 4, MinNodes: 2, MaxNodes: 2})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/distributed/groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var group api.DistributedGroup
	_ = json.NewDecoder(resp.Body).Decode(&group)
	resp.Body.Close()
	renew, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/distributed/groups/"+group.GroupID+"/renew", nil)
	renew.Header.Set("X-AIPool-Client-Secret", "secret")
	renewResp, err := http.DefaultClient.Do(renew)
	if err != nil {
		t.Fatal(err)
	}
	renewResp.Body.Close()
	if renewResp.StatusCode != http.StatusOK {
		t.Fatalf("renew failed: %s", renewResp.Status)
	}
}

func TestDistributedGroupUsesFreeSystemMemoryForCPUWorkers(t *testing.T) {
	server := httptest.NewServer(New("secret", time.Minute).Handler())
	defer server.Close()
	for _, id := range []string{"cpu-a", "cpu-b"} {
		registerNode(t, server.URL, api.NodeRegistration{
			NodeID: id, Endpoint: "http://" + id, StageEndpoint: id + ":50052",
			DistributedReady: true, MaxConcurrency: 1, AcceptsModelUploads: true,
			Available: true, Runtime: api.RuntimeStatus{Ready: true},
			Hardware: api.HardwareInventory{MemoryMB: 16_000, MemoryFreeMB: 4_000},
		})
	}
	body, _ := json.Marshal(api.DistributedGroupRequest{
		Model: "cpu-model", ModelDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ModelSize: 6_000 << 20, ModelFormat: "gguf", LayerCount: 4,
		LayerBytes: []int64{1500 << 20, 1500 << 20, 1500 << 20, 1500 << 20}, MinNodes: 2, MaxNodes: 2,
	})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/distributed/groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CPU-only group was not scheduled from free system memory: %s", resp.Status)
	}
}
