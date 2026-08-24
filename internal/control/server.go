package control

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/local/aipool/internal/api"
	"github.com/local/aipool/internal/auth"
	"github.com/local/aipool/internal/distributed"
	"github.com/local/aipool/internal/httpx"
)

type Server struct {
	hostSecret      string
	clientSecret    string
	leaseSecret     []byte
	ttl             time.Duration
	mu              sync.RWMutex
	nodes           map[string]api.Node
	reservations    map[string]reservation
	groups          map[string]groupReservation
	nodeCredentials map[string]NodeCredential
}

type NodeCredential struct {
	RegistrationSecret string `json:"host_secret"`
	LeaseSecret        string `json:"lease_secret"`
	Scope              string `json:"scope,omitempty"`
}

type reservation struct {
	nodeID    string
	expiresAt time.Time
}

type groupReservation struct {
	reservationIDs []string
	expiresAt      time.Time
}

func New(secret string, ttl time.Duration) *Server {
	return NewWithSecrets(secret, secret, secret, ttl)
}

func NewWithSecrets(hostSecret, clientSecret, leaseSecret string, ttl time.Duration) *Server {
	return NewWithNodeCredentials(hostSecret, clientSecret, leaseSecret, ttl, nil)
}

func NewWithNodeCredentials(hostSecret, clientSecret, leaseSecret string, ttl time.Duration, credentials map[string]NodeCredential) *Server {
	return &Server{hostSecret: hostSecret, clientSecret: clientSecret, leaseSecret: []byte(leaseSecret), ttl: ttl, nodes: make(map[string]api.Node), reservations: make(map[string]reservation), groups: make(map[string]groupReservation), nodeCredentials: credentials}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/nodes/register", s.register)
	mux.HandleFunc("POST /v1/leases", s.createLease)
	mux.HandleFunc("POST /v1/leases/{leaseID}/release", s.releaseLease)
	mux.HandleFunc("GET /v1/nodes", s.listNodes)
	mux.HandleFunc("POST /v1/distributed/groups", s.createDistributedGroup)
	mux.HandleFunc("POST /v1/distributed/groups/{groupID}/release", s.releaseDistributedGroup)
	mux.HandleFunc("POST /v1/distributed/groups/{groupID}/renew", s.renewDistributedGroup)
	return mux
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req api.NodeRegistration
	if err := httpx.DecodeJSON(r, &req); err != nil || req.NodeID == "" || req.Endpoint == "" || (len(req.Models) == 0 && !req.AcceptsModelUploads) {
		httpx.Error(w, http.StatusBadRequest, "node_id, endpoint and either models or model upload support are required")
		return
	}
	expectedSecret := s.hostSecret
	if len(s.nodeCredentials) > 0 {
		credential, ok := s.nodeCredentials[req.NodeID]
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "node is not authorized in this pool")
			return
		}
		expectedSecret = credential.RegistrationSecret
		if credential.Scope != "" {
			req.Scope = credential.Scope
		} else {
			req.Scope = "remote"
		}
	}
	if !auth.EqualSecret(r.Header.Get("X-AIPool-Host-Secret"), expectedSecret) {
		httpx.Error(w, http.StatusUnauthorized, "host registration secret is invalid")
		return
	}
	if req.Scope == "" {
		req.Scope = "remote"
	}
	if req.Scope != "local" && req.Scope != "remote" {
		httpx.Error(w, http.StatusBadRequest, "scope must be local or remote")
		return
	}
	if req.MaxConcurrency <= 0 {
		req.MaxConcurrency = 1
	}
	if req.ActiveTasks < 0 || req.ActiveTasks > req.MaxConcurrency {
		httpx.Error(w, http.StatusBadRequest, "active_tasks must be within max_concurrency")
		return
	}
	if req.DistributedReady && req.StageEndpoint == "" {
		httpx.Error(w, http.StatusBadRequest, "stage_endpoint is required for distributed nodes")
		return
	}
	node := api.Node{NodeID: req.NodeID, Endpoint: req.Endpoint, Models: slices.Clone(req.Models), Hardware: req.Hardware, Runtime: req.Runtime, LastSeen: time.Now().UTC(), Available: req.Available, AcceptsModelUploads: req.AcceptsModelUploads, Scope: req.Scope, MaxConcurrency: req.MaxConcurrency, ActiveTasks: req.ActiveTasks, DistributedReady: req.DistributedReady, StageEndpoint: req.StageEndpoint}
	s.mu.Lock()
	s.nodes[node.NodeID] = node
	s.mu.Unlock()
	httpx.JSON(w, http.StatusOK, node)
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	s.mu.Lock()
	s.expireReservationsLocked(time.Now())
	nodes := make([]api.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		node.Runtime.URL = ""
		node.ReservedTasks = s.reservedForNodeLocked(node.NodeID)
		nodes = append(nodes, node)
	}
	s.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	httpx.JSON(w, http.StatusOK, map[string]any{"data": nodes})
}

func (s *Server) createLease(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	var req api.LeaseRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Model == "" || (req.ModelDigest != "" && (len(req.ModelDigest) != 64 || req.ModelSize <= 0 || req.ModelFormat != "gguf")) {
		httpx.Error(w, http.StatusBadRequest, "valid model metadata is required")
		return
	}
	if req.Scope != "" && req.Scope != "local" && req.Scope != "remote" {
		httpx.Error(w, http.StatusBadRequest, "scope must be local or remote")
		return
	}
	excluded := make(map[string]bool, len(req.ExcludeNodeIDs))
	for _, nodeID := range req.ExcludeNodeIDs {
		excluded[nodeID] = true
	}
	s.mu.Lock()
	s.expireReservationsLocked(time.Now())
	var selected api.Node
	selectedLoad := 2.0
	for _, node := range s.nodes {
		modelSupported := slices.Contains(node.Models, req.Model)
		if req.ModelDigest != "" {
			modelSupported = node.AcceptsModelUploads
		}
		reserved := s.reservedForNodeLocked(node.NodeID)
		active := max(node.ActiveTasks, reserved)
		matchesTarget := req.NodeID == "" || node.NodeID == req.NodeID
		matchesScope := req.Scope == "" || node.Scope == req.Scope
		if node.Available && node.Runtime.Ready && modelSupported && time.Since(node.LastSeen) < 90*time.Second && hasVRAM(node, req.MinVRAMMB) && matchesTarget && matchesScope && !excluded[node.NodeID] && active < node.MaxConcurrency {
			load := float64(active) / float64(node.MaxConcurrency)
			if selected.NodeID == "" || load < selectedLoad || (load == selectedLoad && availableVRAM(node) > availableVRAM(selected)) || (load == selectedLoad && availableVRAM(node) == availableVRAM(selected) && node.NodeID < selected.NodeID) {
				selected = node
				selectedLoad = load
			}
		}
	}
	if selected.NodeID == "" {
		s.mu.Unlock()
		httpx.Error(w, http.StatusServiceUnavailable, "no authorized host is available for this model")
		return
	}
	leaseSecret := s.leaseSecret
	if credential, ok := s.nodeCredentials[selected.NodeID]; ok {
		leaseSecret = []byte(credential.LeaseSecret)
	}
	token, claims, err := auth.IssueModel(leaseSecret, selected.NodeID, req.Model, req.ModelDigest, req.ModelSize, s.leaseTTL(req))
	if err != nil {
		s.mu.Unlock()
		httpx.Error(w, http.StatusInternalServerError, "could not issue lease")
		return
	}
	reservationExpiry := time.Now().Add(15 * time.Minute)
	if claimExpiry := time.Unix(claims.ExpiresAt, 0); claimExpiry.Before(reservationExpiry) {
		reservationExpiry = claimExpiry
	}
	s.reservations[claims.Nonce] = reservation{nodeID: selected.NodeID, expiresAt: reservationExpiry}
	s.mu.Unlock()
	httpx.JSON(w, http.StatusCreated, api.Lease{LeaseID: claims.Nonce, NodeID: selected.NodeID, Endpoint: selected.Endpoint, Model: req.Model, Token: token, ExpiresAt: claims.ExpiresAt, ModelDigest: req.ModelDigest, ModelSize: req.ModelSize, ModelFormat: req.ModelFormat})
}

func (s *Server) releaseLease(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	leaseID := r.PathValue("leaseID")
	if leaseID == "" {
		httpx.Error(w, http.StatusBadRequest, "lease ID is required")
		return
	}
	s.mu.Lock()
	_, existed := s.reservations[leaseID]
	delete(s.reservations, leaseID)
	s.mu.Unlock()
	httpx.JSON(w, http.StatusOK, map[string]any{"released": existed})
}

func (s *Server) createDistributedGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	var req api.DistributedGroupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.Model == "" || req.LayerCount < 2 || req.MinNodes < 0 || req.MaxNodes < 0 || (req.ModelDigest != "" && (len(req.ModelDigest) != 64 || req.ModelSize <= 0 || req.ModelFormat != "gguf")) {
		httpx.Error(w, http.StatusBadRequest, "valid distributed model metadata and layer_count are required")
		return
	}
	if req.Scope != "" && req.Scope != "local" && req.Scope != "remote" {
		httpx.Error(w, http.StatusBadRequest, "scope must be local or remote")
		return
	}
	if req.MinNodes == 0 {
		req.MinNodes = 2
	}
	if req.MaxNodes == 0 {
		req.MaxNodes = req.MinNodes
	}
	s.mu.Lock()
	s.expireReservationsLocked(time.Now())
	candidates := make([]distributed.Candidate, 0, len(s.nodes))
	for _, node := range s.nodes {
		reserved := s.reservedForNodeLocked(node.NodeID)
		active := max(node.ActiveTasks, reserved)
		modelSupported := slices.Contains(node.Models, req.Model) || (req.ModelDigest != "" && node.DistributedReady)
		if node.Available && node.Runtime.Ready && node.DistributedReady && modelSupported && time.Since(node.LastSeen) < 90*time.Second && active < node.MaxConcurrency && (req.Scope == "" || node.Scope == req.Scope) {
			candidates = append(candidates, distributed.Candidate{Node: node, Capacity: nodeMemoryCapacity(node), Load: float64(active) / float64(node.MaxConcurrency)})
		}
	}
	var plan []api.StageAssignment
	var err error
	if len(req.LayerBytes) > 0 {
		if len(req.LayerBytes) != req.LayerCount {
			s.mu.Unlock()
			httpx.Error(w, http.StatusBadRequest, "layer_bytes length must equal layer_count")
			return
		}
		plan, err = distributed.PlanLayers(candidates, req.LayerBytes, req.MinNodes, req.MaxNodes)
	} else {
		plan, err = distributed.Plan(candidates, req.LayerCount, req.ModelSize, req.MinNodes, req.MaxNodes)
	}
	if err != nil {
		s.mu.Unlock()
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	groupID, err := randomIdentifier()
	if err != nil {
		s.mu.Unlock()
		httpx.Error(w, http.StatusInternalServerError, "could not create distributed group")
		return
	}
	expires := time.Now().Add(15 * time.Minute)
	reservationIDs := make([]string, 0, len(plan))
	for i := range plan {
		leaseSecret := s.leaseSecret
		if credential, ok := s.nodeCredentials[plan[i].NodeID]; ok {
			leaseSecret = []byte(credential.LeaseSecret)
		}
		token, claims, issueErr := auth.IssueStage(leaseSecret, plan[i].NodeID, req.Model, req.ModelDigest, req.ModelSize, groupID, i, plan[i].LayerStart, plan[i].LayerEnd, s.leaseTTL(api.LeaseRequest{Model: req.Model, ModelSize: req.ModelSize}))
		if issueErr != nil {
			for _, id := range reservationIDs {
				delete(s.reservations, id)
			}
			s.mu.Unlock()
			httpx.Error(w, http.StatusInternalServerError, "could not issue distributed stage lease")
			return
		}
		plan[i].ReservationID, plan[i].Token = claims.Nonce, token
		s.reservations[claims.Nonce] = reservation{nodeID: plan[i].NodeID, expiresAt: expires}
		reservationIDs = append(reservationIDs, claims.Nonce)
	}
	s.groups[groupID] = groupReservation{reservationIDs: reservationIDs, expiresAt: expires}
	s.mu.Unlock()
	httpx.JSON(w, http.StatusCreated, api.DistributedGroup{GroupID: groupID, Model: req.Model, ModelDigest: req.ModelDigest, LayerCount: req.LayerCount, HiddenSize: req.HiddenSize, Stages: plan, ExpiresAt: expires.Unix()})
}

func (s *Server) releaseDistributedGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	groupID := r.PathValue("groupID")
	s.mu.Lock()
	group, existed := s.groups[groupID]
	if existed {
		for _, id := range group.reservationIDs {
			delete(s.reservations, id)
		}
		delete(s.groups, groupID)
	}
	s.mu.Unlock()
	httpx.JSON(w, http.StatusOK, map[string]any{"released": existed})
}

func (s *Server) renewDistributedGroup(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeClient(w, r) {
		return
	}
	groupID := r.PathValue("groupID")
	now := time.Now()
	s.mu.Lock()
	s.expireReservationsLocked(now)
	group, existed := s.groups[groupID]
	if existed {
		group.expiresAt = now.Add(15 * time.Minute)
		s.groups[groupID] = group
		for _, id := range group.reservationIDs {
			if item, ok := s.reservations[id]; ok {
				item.expiresAt = group.expiresAt
				s.reservations[id] = item
			}
		}
	}
	s.mu.Unlock()
	if !existed {
		httpx.Error(w, http.StatusNotFound, "distributed group was not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"group_id": groupID, "expires_at": group.expiresAt.Unix()})
}

func (s *Server) expireReservationsLocked(now time.Time) {
	for leaseID, item := range s.reservations {
		if !item.expiresAt.After(now) {
			delete(s.reservations, leaseID)
		}
	}
	for groupID, group := range s.groups {
		if !group.expiresAt.After(now) {
			delete(s.groups, groupID)
		}
	}
}

func nodeMemoryCapacity(node api.Node) int {
	capacity := availableVRAM(node)
	if capacity == 0 {
		capacity = int(node.Hardware.MemoryFreeMB)
		if capacity == 0 {
			capacity = int(node.Hardware.MemoryMB)
		}
	}
	return capacity
}

func randomIdentifier() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Server) reservedForNodeLocked(nodeID string) int {
	count := 0
	for _, item := range s.reservations {
		if item.nodeID == nodeID {
			count++
		}
	}
	return count
}

func (s *Server) leaseTTL(req api.LeaseRequest) time.Duration {
	ttl := s.ttl
	if req.ModelSize > 0 {
		// Allow a first-time upload to run at roughly 512 KiB/s plus startup margin.
		uploadTTL := time.Duration(req.ModelSize/(512<<10))*time.Second + 10*time.Minute
		if uploadTTL > 6*time.Hour {
			uploadTTL = 6 * time.Hour
		}
		if uploadTTL > ttl {
			ttl = uploadTTL
		}
	}
	return ttl
}

func (s *Server) authorizeClient(w http.ResponseWriter, r *http.Request) bool {
	if !auth.EqualSecret(r.Header.Get("X-AIPool-Client-Secret"), s.clientSecret) {
		httpx.Error(w, http.StatusUnauthorized, "requester secret is invalid")
		return false
	}
	return true
}

func availableVRAM(node api.Node) int {
	maximum := 0
	for _, gpu := range node.Hardware.GPUDevices {
		if gpu.MemoryFreeMB > maximum {
			maximum = gpu.MemoryFreeMB
		}
	}
	return maximum
}

func hasVRAM(node api.Node, minimum int) bool {
	if minimum <= 0 {
		return true
	}
	for _, gpu := range node.Hardware.GPUDevices {
		if gpu.MemoryFreeMB >= minimum {
			return true
		}
	}
	return false
}
