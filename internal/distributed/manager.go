package distributed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/gguf"
)

type ManagerConfig struct {
	ControlURL   string
	ClientSecret string
	Executable   string
	MinNodes     int
	MaxNodes     int
	Port         int
	ContextSize  int
	HTTPClient   *http.Client
}

type Manager struct {
	cfg       ManagerConfig
	mu        sync.Mutex
	runtime   *LlamaRPC
	group     api.DistributedGroup
	digest    string
	stopRenew chan struct{}
}

func NewManager(cfg ManagerConfig) *Manager {
	if cfg.MinNodes < 2 {
		cfg.MinNodes = 2
	}
	if cfg.MaxNodes < cfg.MinNodes {
		cfg.MaxNodes = cfg.MinNodes
	}
	if cfg.Port == 0 {
		cfg.Port = 18082
	}
	if cfg.ContextSize == 0 {
		cfg.ContextSize = 4096
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	cfg.ControlURL = strings.TrimRight(cfg.ControlURL, "/")
	return &Manager{cfg: cfg}
}

func (m *Manager) Ensure(ctx context.Context, modelID, path, digest string, size int64) (string, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtime != nil && m.digest == digest {
		address := "http://" + netJoin("127.0.0.1", m.cfg.Port)
		if healthy(ctx, m.cfg.HTTPClient, address) {
			return address, groupNodes(m.group), nil
		}
	}
	m.stopLocked()
	metadata, err := gguf.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	layerBytes := append([]int64(nil), metadata.LayerBytes...)
	nonLayerBytes := metadata.NonLayerBytes
	if nonLayerBytes > 0 {
		// llama.cpp RPC places embeddings/output tensors too. Account for their
		// memory in the plan even though they are not Transformer blocks.
		layerBytes[0] += (nonLayerBytes + 1) / 2
		layerBytes[len(layerBytes)-1] += nonLayerBytes / 2
	}
	request := api.DistributedGroupRequest{Model: modelID, ModelDigest: digest, ModelSize: size, ModelFormat: "gguf", LayerCount: metadata.LayerCount, HiddenSize: metadata.EmbeddingLength, LayerBytes: layerBytes, MinNodes: m.cfg.MinNodes, MaxNodes: m.cfg.MaxNodes}
	group, err := m.createGroup(ctx, request)
	if err != nil {
		return "", nil, err
	}
	servers := make([]string, len(group.Stages))
	split := make([]string, len(group.Stages))
	for i, assigned := range group.Stages {
		servers[i] = strings.TrimPrefix(assigned.Endpoint, "tcp://")
		split[i] = strconv.FormatInt(max(assigned.TensorBytes, 1), 10)
	}
	runtime := &LlamaRPC{Executable: m.cfg.Executable, ModelPath: path, Host: "127.0.0.1", Port: m.cfg.Port, Context: m.cfg.ContextSize, GPULayers: 999, RPCServers: servers, ExtraArgs: []string{"--alias", modelID, "--split-mode", "layer", "--tensor-split", strings.Join(split, ",")}}
	address, err := runtime.Start(ctx)
	if err != nil {
		m.releaseGroup(group.GroupID)
		return "", nil, err
	}
	m.runtime, m.group, m.digest = runtime, group, digest
	m.startRenewLocked(group.GroupID)
	return address, groupNodes(group), nil
}

func (m *Manager) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.stopLocked(); return nil }
func (m *Manager) stopLocked() {
	if m.stopRenew != nil {
		close(m.stopRenew)
		m.stopRenew = nil
	}
	if m.runtime != nil {
		_ = m.runtime.Close()
		m.runtime = nil
	}
	if m.group.GroupID != "" {
		m.releaseGroup(m.group.GroupID)
	}
	m.group = api.DistributedGroup{}
	m.digest = ""
}
func (m *Manager) createGroup(ctx context.Context, request api.DistributedGroupRequest) (api.DistributedGroup, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return api.DistributedGroup{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ControlURL+"/v1/distributed/groups", bytes.NewReader(data))
	if err != nil {
		return api.DistributedGroup{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Client-Secret", m.cfg.ClientSecret)
	resp, err := m.cfg.HTTPClient.Do(req)
	if err != nil {
		return api.DistributedGroup{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return api.DistributedGroup{}, fmt.Errorf("distributed group request returned %s", resp.Status)
	}
	var group api.DistributedGroup
	if err := json.NewDecoder(resp.Body).Decode(&group); err != nil {
		return api.DistributedGroup{}, err
	}
	if group.GroupID == "" || len(group.Stages) < 2 {
		return api.DistributedGroup{}, fmt.Errorf("control returned an invalid distributed group")
	}
	for i, stage := range group.Stages {
		endpoint := strings.TrimPrefix(stage.Endpoint, "tcp://")
		parsed, err := url.Parse("tcp://" + endpoint)
		if err != nil || parsed.Hostname() == "" || parsed.Port() == "" || stage.NodeID == "" || stage.TensorBytes <= 0 {
			m.releaseGroup(group.GroupID)
			return api.DistributedGroup{}, fmt.Errorf("control returned invalid stage %d", i)
		}
	}
	return group, nil
}
func (m *Manager) releaseGroup(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ControlURL+"/v1/distributed/groups/"+url.PathEscape(id)+"/release", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-AIPool-Client-Secret", m.cfg.ClientSecret)
	if resp, err := m.cfg.HTTPClient.Do(req); err == nil {
		resp.Body.Close()
	}
}
func (m *Manager) startRenewLocked(id string) {
	stop := make(chan struct{})
	m.stopRenew = stop
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ControlURL+"/v1/distributed/groups/"+url.PathEscape(id)+"/renew", nil)
				if err != nil {
					cancel()
					continue
				}
				req.Header.Set("X-AIPool-Client-Secret", m.cfg.ClientSecret)
				if resp, err := m.cfg.HTTPClient.Do(req); err == nil {
					resp.Body.Close()
				}
				cancel()
			}
		}
	}()
}
func groupNodes(group api.DistributedGroup) []string {
	nodes := make([]string, len(group.Stages))
	for i, item := range group.Stages {
		nodes[i] = item.NodeID
	}
	return nodes
}
func healthy(ctx context.Context, client *http.Client, address string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}
func netJoin(host string, port int) string { return host + ":" + strconv.Itoa(port) }
