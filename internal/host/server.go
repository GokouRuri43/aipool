package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/auth"
	"github.com/local/aipool/internal/hardware"
	"github.com/local/aipool/internal/httpx"
	"github.com/local/aipool/internal/modelcache"
	"github.com/local/aipool/internal/openai"
)

type Config struct {
	NodeID             string
	Endpoint           string
	ControlURL         string
	Secret             string
	RegistrationSecret string
	LeaseSecret        string
	Models             []string
	RuntimeURL         string
	RuntimeKind        string
	RuntimeModel       string
	HTTPClient         *http.Client
	Inventory          func() api.HardwareInventory
	ModelStore         *modelcache.Store
	RuntimeManager     ManagedRuntime
	MaxUploadChunk     int64
	Scope              string
	MaxConcurrency     int
	DistributedReady   bool
	StageEndpoint      string
}

type ManagedRuntime interface {
	Available() error
	Ensure(ctx context.Context, modelID, digest, modelPath string) (string, error)
}

type Server struct {
	cfg         Config
	client      *http.Client
	seq         atomic.Uint64
	managedMu   sync.Mutex
	activeTasks atomic.Int64
}

func New(cfg Config) *Server {
	if cfg.RegistrationSecret == "" {
		cfg.RegistrationSecret = cfg.Secret
	}
	if cfg.LeaseSecret == "" {
		cfg.LeaseSecret = cfg.Secret
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 20, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		}}
	}
	if cfg.Inventory == nil {
		cfg.Inventory = hardware.DetectWithTimeout
	}
	if cfg.RuntimeKind == "" {
		if cfg.RuntimeManager != nil {
			cfg.RuntimeKind = "managed-llama.cpp"
		} else if cfg.RuntimeURL == "" {
			cfg.RuntimeKind = "mock"
		} else {
			cfg.RuntimeKind = "openai-compatible"
		}
	}
	if cfg.MaxUploadChunk <= 0 {
		cfg.MaxUploadChunk = 8 << 20
	}
	if cfg.Scope == "" {
		cfg.Scope = "remote"
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1
	}
	return &Server{cfg: cfg, client: client}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "node_id": s.cfg.NodeID})
	})
	mux.HandleFunc("GET /v1/models", s.authorize(s.models))
	mux.HandleFunc("POST /v1/chat/completions", s.authorize(s.chat))
	mux.HandleFunc("GET /v1/model-cache/{digest}", s.modelStatus)
	mux.HandleFunc("PUT /v1/model-cache/{digest}", s.modelUpload)
	return mux
}

func (s *Server) Register(ctx context.Context) error {
	runtimeStatus := s.RuntimeStatus(ctx)
	body, _ := json.Marshal(api.NodeRegistration{
		NodeID: s.cfg.NodeID, Endpoint: s.cfg.Endpoint, Models: s.cfg.Models,
		Hardware: s.cfg.Inventory(), Runtime: runtimeStatus, Available: runtimeStatus.Ready,
		AcceptsModelUploads: s.cfg.ModelStore != nil,
		Scope:               s.cfg.Scope, MaxConcurrency: s.cfg.MaxConcurrency, ActiveTasks: int(s.activeTasks.Load()),
		DistributedReady: s.cfg.DistributedReady, StageEndpoint: s.cfg.StageEndpoint,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.ControlURL, "/")+"/v1/nodes/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIPool-Host-Secret", s.cfg.RegistrationSecret)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("control registration returned %s", resp.Status)
	}
	return nil
}

func (s *Server) RuntimeStatus(ctx context.Context) api.RuntimeStatus {
	status := api.RuntimeStatus{Kind: s.cfg.RuntimeKind, URL: s.cfg.RuntimeURL}
	if s.cfg.RuntimeManager != nil {
		if err := s.cfg.RuntimeManager.Available(); err != nil {
			status.Message = err.Error()
			return status
		}
		status.Ready, status.Message = true, "managed runtime ready to load uploaded models"
		return status
	}
	if s.cfg.RuntimeURL == "" {
		status.Ready = true
		status.Message = "built-in mock runtime"
		return status
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.RuntimeURL, "/")+"/health", nil)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	resp, err := s.client.Do(req)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	defer resp.Body.Close()
	status.Ready = resp.StatusCode/100 == 2
	if status.Ready {
		status.Message = "runtime healthy"
	} else {
		status.Message = "runtime returned " + resp.Status
	}
	return status
}

func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := auth.Verify([]byte(s.cfg.LeaseSecret), httpx.Bearer(r))
		if err != nil || claims.NodeID != s.cfg.NodeID {
			httpx.Error(w, http.StatusUnauthorized, "valid host lease required")
			return
		}
		if r.Method == http.MethodPost {
			var probe struct {
				Model string `json:"model"`
			}
			data, err := readBody(r)
			if err != nil || json.Unmarshal(data, &probe) != nil || probe.Model != claims.Model || (claims.ModelDigest != "" && r.Header.Get("X-AIPool-Model-Digest") != claims.ModelDigest) {
				httpx.Error(w, http.StatusForbidden, "lease does not permit the requested model")
				return
			}
			r.Body = ioNopCloser{bytes.NewReader(data)}
		}
		next(w, r)
	}
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	data := make([]map[string]any, 0, len(s.cfg.Models))
	for _, model := range s.cfg.Models {
		data = append(data, map[string]any{"id": model, "object": "model", "owned_by": s.cfg.NodeID})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if !s.acquireTask() {
		w.Header().Set("Retry-After", "1")
		httpx.Error(w, http.StatusTooManyRequests, "provider concurrency limit reached")
		return
	}
	defer s.activeTasks.Add(-1)
	claims, _ := auth.Verify([]byte(s.cfg.LeaseSecret), httpx.Bearer(r))
	if claims.ModelDigest != "" {
		if s.cfg.ModelStore == nil {
			httpx.Error(w, http.StatusConflict, "host does not accept uploaded models")
			return
		}
		status, err := s.cfg.ModelStore.Status(claims.ModelDigest, claims.ModelSize)
		if err != nil || !status.Ready {
			httpx.Error(w, http.StatusConflict, "leased model has not finished uploading")
			return
		}
		if s.cfg.RuntimeManager != nil {
			s.managedMu.Lock()
			defer s.managedMu.Unlock()
			runtimeURL, err := s.cfg.RuntimeManager.Ensure(r.Context(), claims.Model, claims.ModelDigest, s.cfg.ModelStore.Path(claims.ModelDigest))
			if err != nil {
				httpx.Error(w, http.StatusBadGateway, "managed runtime could not load model: "+err.Error())
				return
			}
			s.forwardRuntimeURL(w, r, runtimeURL, claims.Model)
			return
		}
	}
	if s.cfg.RuntimeURL != "" {
		s.forwardRuntime(w, r)
		return
	}
	var req openai.ChatRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Model == "" || len(req.Messages) == 0 || !slices.Contains(s.cfg.Models, req.Model) {
		httpx.Error(w, http.StatusBadRequest, "valid model and messages are required")
		return
	}
	prompt := messageText(req.Messages[len(req.Messages)-1].Content)
	reply := "这是来自共享节点 " + s.cfg.NodeID + " 的模拟回复：" + prompt
	id := fmt.Sprintf("chatcmpl-aipool-%d", s.seq.Add(1))
	if !req.Stream {
		httpx.JSON(w, http.StatusOK, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
		return
	}
	streamMock(w, id, req.Model, reply)
}

func (s *Server) acquireTask() bool {
	for {
		active := s.activeTasks.Load()
		if active >= int64(s.cfg.MaxConcurrency) {
			return false
		}
		if s.activeTasks.CompareAndSwap(active, active+1) {
			return true
		}
	}
}

func (s *Server) forwardRuntime(w http.ResponseWriter, r *http.Request) {
	s.forwardRuntimeURL(w, r, s.cfg.RuntimeURL, s.cfg.RuntimeModel)
}

func (s *Server) forwardRuntimeURL(w http.ResponseWriter, r *http.Request, runtimeURL, runtimeModel string) {
	data, err := readBody(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid body")
		return
	}
	if runtimeModel != "" {
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid body")
			return
		}
		payload["model"] = runtimeModel
		data, _ = json.Marshal(payload)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(runtimeURL, "/")+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "runtime request failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "runtime unavailable")
		return
	}
	defer resp.Body.Close()
	copyResponse(w, resp)
}

func (s *Server) modelStatus(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authorizeModelTransfer(w, r)
	if !ok {
		return
	}
	status, err := s.cfg.ModelStore.Status(claims.ModelDigest, claims.ModelSize)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

func (s *Server) modelUpload(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authorizeModelTransfer(w, r)
	if !ok {
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("X-AIPool-Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		httpx.Error(w, http.StatusBadRequest, "valid X-AIPool-Upload-Offset is required")
		return
	}
	status, err := s.cfg.ModelStore.Append(claims.ModelDigest, claims.ModelSize, offset, r.Body, s.cfg.MaxUploadChunk)
	if err != nil {
		if mismatch, yes := err.(*modelcache.OffsetError); yes {
			w.Header().Set("X-AIPool-Expected-Offset", strconv.FormatInt(mismatch.Expected, 10))
			httpx.Error(w, http.StatusConflict, err.Error())
			return
		}
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

func (s *Server) authorizeModelTransfer(w http.ResponseWriter, r *http.Request) (api.LeaseClaims, bool) {
	if s.cfg.ModelStore == nil {
		httpx.Error(w, http.StatusNotFound, "model upload is disabled")
		return api.LeaseClaims{}, false
	}
	claims, err := auth.Verify([]byte(s.cfg.LeaseSecret), httpx.Bearer(r))
	if err != nil || claims.NodeID != s.cfg.NodeID || claims.ModelDigest == "" || claims.ModelSize <= 0 || r.PathValue("digest") != claims.ModelDigest {
		httpx.Error(w, http.StatusUnauthorized, "valid model-bound host lease required")
		return api.LeaseClaims{}, false
	}
	return claims, true
}

func streamMock(w http.ResponseWriter, id, model, reply string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Error(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	for _, token := range strings.Fields(reply) {
		chunk, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": token + " "}, "finish_reason": nil}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
	}
	final, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", final)
	flusher.Flush()
}

type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }

func messageText(v any) string {
	if text, ok := v.(string); ok {
		return text
	}
	b, _ := json.Marshal(v)
	return string(b)
}
