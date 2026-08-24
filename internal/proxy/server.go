package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/distributed"
	"github.com/local/aipool/internal/httpx"
	"github.com/local/aipool/internal/modelcatalog"
)

type Server struct {
	controlURL         string
	clientSecret       string
	controlClient      *http.Client
	hostClient         *http.Client
	preflightTimeout   time.Duration
	catalog            *modelcatalog.Catalog
	uploadChunkSize    int64
	uploadMu           sync.Mutex
	uploadLocks        map[string]*sync.Mutex
	distributed        *distributed.Manager
	distributedDefault bool
}

type Config struct {
	ControlURL            string
	ClientSecret          string
	ControlTimeout        time.Duration
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	PreflightTimeout      time.Duration
	Catalog               *modelcatalog.Catalog
	UploadChunkSize       int64
	Distributed           *distributed.Manager
	DistributedDefault    bool
}

func New(controlURL string, clientSecret ...string) *Server {
	secret := "dev-only-change-me"
	if len(clientSecret) > 0 && clientSecret[0] != "" {
		secret = clientSecret[0]
	}
	return NewWithConfig(Config{ControlURL: controlURL, ClientSecret: secret})
}

func NewWithConfig(cfg Config) *Server {
	if cfg.ControlTimeout <= 0 {
		cfg.ControlTimeout = 10 * time.Second
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 3 * time.Second
	}
	if cfg.ResponseHeaderTimeout <= 0 {
		cfg.ResponseHeaderTimeout = 3 * time.Minute
	}
	if cfg.PreflightTimeout <= 0 {
		cfg.PreflightTimeout = 3 * time.Second
	}
	if cfg.UploadChunkSize <= 0 {
		cfg.UploadChunkSize = 8 << 20
	}
	dialer := &net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &Server{
		controlURL: strings.TrimRight(cfg.ControlURL, "/"), clientSecret: cfg.ClientSecret,
		controlClient: &http.Client{Transport: transport.Clone(), Timeout: cfg.ControlTimeout},
		hostClient:    &http.Client{Transport: transport}, preflightTimeout: cfg.PreflightTimeout,
		catalog: cfg.Catalog, uploadChunkSize: cfg.UploadChunkSize, uploadLocks: map[string]*sync.Mutex{},
		distributed: cfg.Distributed, distributedDefault: cfg.DistributedDefault,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	return mux
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if s.catalog != nil {
		models := make([]map[string]any, 0, s.catalog.Len())
		for _, model := range s.catalog.Entries() {
			models = append(models, map[string]any{"id": model.ID, "object": "model", "owned_by": "local-user", "aipool_digest": model.Digest, "aipool_size": model.Size})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, s.controlURL+"/v1/nodes", nil)
	s.authorizeControl(req)
	resp, err := s.controlClient.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "control plane unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		httpx.Error(w, http.StatusBadGateway, "control plane rejected model discovery")
		return
	}
	var payload struct {
		Data []api.Node `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		httpx.Error(w, http.StatusBadGateway, "invalid control response")
		return
	}
	seen := map[string]bool{}
	models := []map[string]any{}
	for _, node := range payload.Data {
		if !node.Available || !node.Runtime.Ready || time.Since(node.LastSeen) >= 90*time.Second {
			continue
		}
		for _, model := range node.Models {
			if !seen[model] {
				seen[model] = true
				models = append(models, map[string]any{"id": model, "object": "model", "owned_by": "aipool"})
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid body")
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Model == "" {
		httpx.Error(w, http.StatusBadRequest, "model is required")
		return
	}
	minimumVRAM := 0
	if value := r.Header.Get("X-AIPool-Min-VRAM-MB"); value != "" {
		minimumVRAM, err = strconv.Atoi(value)
		if err != nil || minimumVRAM < 0 {
			httpx.Error(w, http.StatusBadRequest, "X-AIPool-Min-VRAM-MB must be a non-negative integer")
			return
		}
	}
	entry, localModel := s.catalog.Lookup(probe.Model)
	if s.catalog != nil && !localModel {
		httpx.Error(w, http.StatusNotFound, "model is not present in the requester's local catalog")
		return
	}
	execution := strings.ToLower(strings.TrimSpace(r.Header.Get("X-AIPool-Execution")))
	if execution == "" && s.distributedDefault {
		execution = "distributed"
	}
	if execution != "" && execution != "single" && execution != "distributed" && execution != "auto" {
		httpx.Error(w, http.StatusBadRequest, "X-AIPool-Execution must be single, distributed or auto")
		return
	}
	if localModel && s.distributed != nil && (execution == "distributed" || execution == "auto") {
		runtimeURL, nodes, err := s.distributed.Ensure(r.Context(), probe.Model, entry.Path, entry.Digest, entry.Size)
		if err != nil {
			if execution == "distributed" {
				httpx.Error(w, http.StatusServiceUnavailable, "distributed inference unavailable: "+err.Error())
				return
			}
		} else {
			w.Header().Set("X-AIPool-Execution", "distributed")
			w.Header().Set("X-AIPool-Nodes", strings.Join(nodes, ","))
			s.forwardDistributed(w, r, runtimeURL, body)
			return
		}
	}
	leaseRequest := api.LeaseRequest{Model: probe.Model, MinVRAMMB: minimumVRAM}
	leaseRequest.NodeID = strings.TrimSpace(r.Header.Get("X-AIPool-Node-ID"))
	leaseRequest.Scope = strings.ToLower(strings.TrimSpace(r.Header.Get("X-AIPool-Scope")))
	if leaseRequest.Scope != "" && leaseRequest.Scope != "local" && leaseRequest.Scope != "remote" {
		httpx.Error(w, http.StatusBadRequest, "X-AIPool-Scope must be local or remote")
		return
	}
	if localModel {
		leaseRequest.ModelDigest, leaseRequest.ModelSize, leaseRequest.ModelFormat = entry.Digest, entry.Size, entry.Format
	}
	lease, err := s.leaseReachable(r, leaseRequest)
	if err != nil {
		if _, ok := err.(*reachabilityError); ok {
			httpx.Error(w, http.StatusBadGateway, err.Error())
			return
		}
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer s.releaseLease(lease.LeaseID)
	w.Header().Set("X-AIPool-Node-ID", lease.NodeID)
	if localModel {
		if err := s.ensureModel(r.Context(), lease, entry.Path); err != nil {
			httpx.Error(w, http.StatusBadGateway, "could not synchronize local model: "+err.Error())
			return
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(lease.Endpoint, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "could not create host request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+lease.Token)
	if lease.ModelDigest != "" {
		req.Header.Set("X-AIPool-Model-Digest", lease.ModelDigest)
	}
	resp, err := s.hostClient.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "host unavailable")
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 16<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
}

func (s *Server) forwardDistributed(w http.ResponseWriter, r *http.Request, runtimeURL string, body []byte) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(runtimeURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "could not create distributed runtime request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hostClient.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "distributed runtime unavailable")
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 16<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			_, _ = w.Write(buffer[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (s *Server) leaseReachable(r *http.Request, leaseRequest api.LeaseRequest) (api.Lease, error) {
	var lastErr error
	for attempts := 0; attempts < 3; attempts++ {
		lease, err := s.lease(r, leaseRequest)
		if err != nil {
			if lastErr != nil {
				return api.Lease{}, &reachabilityError{lastErr}
			}
			return api.Lease{}, err
		}
		if err := s.preflight(r.Context(), lease.Endpoint); err == nil {
			return lease, nil
		} else {
			lastErr = err
		}
		s.releaseLease(lease.LeaseID)
		if leaseRequest.NodeID != "" {
			break
		}
		leaseRequest.ExcludeNodeIDs = append(leaseRequest.ExcludeNodeIDs, lease.NodeID)
	}
	return api.Lease{}, &reachabilityError{lastErr}
}

func (s *Server) releaseLease(leaseID string) {
	if leaseID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.controlURL+"/v1/leases/"+url.PathEscape(leaseID)+"/release", nil)
	if err != nil {
		return
	}
	s.authorizeControl(req)
	resp, err := s.controlClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Server) lease(r *http.Request, leaseRequest api.LeaseRequest) (api.Lease, error) {
	body, _ := json.Marshal(leaseRequest)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, s.controlURL+"/v1/leases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.authorizeControl(req)
	resp, err := s.controlClient.Do(req)
	if err != nil {
		return api.Lease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return api.Lease{}, &statusError{status: resp.Status}
	}
	var lease api.Lease
	if err := json.NewDecoder(resp.Body).Decode(&lease); err != nil {
		return api.Lease{}, err
	}
	return lease, nil
}

func (s *Server) ensureModel(ctx context.Context, lease api.Lease, path string) error {
	unlock := s.lockUpload(lease.ModelDigest)
	defer unlock()
	status, err := s.modelStatus(ctx, lease)
	if err != nil {
		return err
	}
	if status.Ready {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	for status.Received < lease.ModelSize {
		remaining := lease.ModelSize - status.Received
		chunkSize := min(remaining, s.uploadChunkSize)
		reader := io.NewSectionReader(file, status.Received, chunkSize)
		endpoint := strings.TrimRight(lease.Endpoint, "/") + "/v1/model-cache/" + lease.ModelDigest
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, reader)
		if err != nil {
			return err
		}
		req.ContentLength = chunkSize
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+lease.Token)
		req.Header.Set("X-AIPool-Upload-Offset", strconv.FormatInt(status.Received, 10))
		resp, err := s.hostClient.Do(req)
		if err != nil {
			return err
		}
		var next api.ModelCacheStatus
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&next)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("host upload returned %s", resp.Status)
		}
		if decodeErr != nil {
			return decodeErr
		}
		if next.Received <= status.Received {
			return fmt.Errorf("host did not advance upload offset")
		}
		status = next
	}
	if !status.Ready {
		return fmt.Errorf("host did not finalize uploaded model")
	}
	return nil
}

func (s *Server) lockUpload(digest string) func() {
	s.uploadMu.Lock()
	lock := s.uploadLocks[digest]
	if lock == nil {
		lock = &sync.Mutex{}
		s.uploadLocks[digest] = lock
	}
	s.uploadMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (s *Server) modelStatus(ctx context.Context, lease api.Lease) (api.ModelCacheStatus, error) {
	endpoint := strings.TrimRight(lease.Endpoint, "/") + "/v1/model-cache/" + lease.ModelDigest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return api.ModelCacheStatus{}, err
	}
	req.Header.Set("Authorization", "Bearer "+lease.Token)
	resp, err := s.hostClient.Do(req)
	if err != nil {
		return api.ModelCacheStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return api.ModelCacheStatus{}, fmt.Errorf("host model status returned %s", resp.Status)
	}
	var status api.ModelCacheStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); err != nil {
		return status, err
	}
	return status, nil
}

func (s *Server) preflight(parent context.Context, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid endpoint")
	}
	ctx, cancel := context.WithTimeout(parent, s.preflightTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := s.hostClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("health check returned %s", resp.Status)
	}
	return nil
}

func (s *Server) authorizeControl(req *http.Request) {
	req.Header.Set("X-AIPool-Client-Secret", s.clientSecret)
}

type statusError struct{ status string }

func (e *statusError) Error() string { return "lease request failed: " + e.status }

type reachabilityError struct{ cause error }

func (e *reachabilityError) Error() string {
	return "selected host is unreachable; no reachable fallback host was found: " + e.cause.Error()
}
