package api

import "time"

type NodeRegistration struct {
	NodeID              string            `json:"node_id"`
	Endpoint            string            `json:"endpoint"`
	Models              []string          `json:"models"`
	Hardware            HardwareInventory `json:"hardware"`
	Runtime             RuntimeStatus     `json:"runtime"`
	Available           bool              `json:"available"`
	AcceptsModelUploads bool              `json:"accepts_model_uploads,omitempty"`
	Scope               string            `json:"scope,omitempty"`
	MaxConcurrency      int               `json:"max_concurrency,omitempty"`
	ActiveTasks         int               `json:"active_tasks,omitempty"`
	DistributedReady    bool              `json:"distributed_ready,omitempty"`
	StageEndpoint       string            `json:"stage_endpoint,omitempty"`
}

type Node struct {
	NodeID              string            `json:"node_id"`
	Endpoint            string            `json:"endpoint"`
	Models              []string          `json:"models"`
	Hardware            HardwareInventory `json:"hardware"`
	Runtime             RuntimeStatus     `json:"runtime"`
	LastSeen            time.Time         `json:"last_seen"`
	Available           bool              `json:"available"`
	AcceptsModelUploads bool              `json:"accepts_model_uploads,omitempty"`
	Scope               string            `json:"scope,omitempty"`
	MaxConcurrency      int               `json:"max_concurrency,omitempty"`
	ActiveTasks         int               `json:"active_tasks,omitempty"`
	ReservedTasks       int               `json:"reserved_tasks,omitempty"`
	DistributedReady    bool              `json:"distributed_ready,omitempty"`
	StageEndpoint       string            `json:"stage_endpoint,omitempty"`
}

type HardwareInventory struct {
	OS           string      `json:"os"`
	Arch         string      `json:"arch"`
	CPULogical   int         `json:"cpu_logical"`
	MemoryMB     uint64      `json:"memory_mb,omitempty"`
	MemoryFreeMB uint64      `json:"memory_free_mb,omitempty"`
	GPUDevices   []GPUDevice `json:"gpu_devices"`
}

type GPUDevice struct {
	Index             int    `json:"index"`
	Name              string `json:"name"`
	UUID              string `json:"uuid"`
	MemoryTotalMB     int    `json:"memory_total_mb"`
	MemoryFreeMB      int    `json:"memory_free_mb"`
	DriverVersion     string `json:"driver_version"`
	ComputeCapability string `json:"compute_capability"`
}

type RuntimeStatus struct {
	Kind    string `json:"kind"`
	URL     string `json:"url,omitempty"`
	Ready   bool   `json:"ready"`
	Message string `json:"message,omitempty"`
}

type LeaseRequest struct {
	Model          string   `json:"model"`
	MinVRAMMB      int      `json:"min_vram_mb,omitempty"`
	ModelDigest    string   `json:"model_digest,omitempty"`
	ModelSize      int64    `json:"model_size,omitempty"`
	ModelFormat    string   `json:"model_format,omitempty"`
	NodeID         string   `json:"node_id,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	ExcludeNodeIDs []string `json:"exclude_node_ids,omitempty"`
}

type Lease struct {
	LeaseID     string `json:"lease_id"`
	NodeID      string `json:"node_id"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	Token       string `json:"token"`
	ExpiresAt   int64  `json:"expires_at"`
	ModelDigest string `json:"model_digest,omitempty"`
	ModelSize   int64  `json:"model_size,omitempty"`
	ModelFormat string `json:"model_format,omitempty"`
}

type LeaseClaims struct {
	NodeID      string `json:"node_id"`
	Model       string `json:"model"`
	ExpiresAt   int64  `json:"expires_at"`
	Nonce       string `json:"nonce"`
	ModelDigest string `json:"model_digest,omitempty"`
	ModelSize   int64  `json:"model_size,omitempty"`
	GroupID     string `json:"group_id,omitempty"`
	StageIndex  int    `json:"stage_index,omitempty"`
	LayerStart  int    `json:"layer_start,omitempty"`
	LayerEnd    int    `json:"layer_end,omitempty"`
}

type LocalModel struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Format string `json:"format"`
}

type ModelCacheStatus struct {
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	Received int64  `json:"received"`
	Ready    bool   `json:"ready"`
}

type DistributedGroupRequest struct {
	Model       string  `json:"model"`
	ModelDigest string  `json:"model_digest,omitempty"`
	ModelSize   int64   `json:"model_size,omitempty"`
	ModelFormat string  `json:"model_format,omitempty"`
	LayerCount  int     `json:"layer_count"`
	HiddenSize  int     `json:"hidden_size,omitempty"`
	MinNodes    int     `json:"min_nodes,omitempty"`
	MaxNodes    int     `json:"max_nodes,omitempty"`
	Scope       string  `json:"scope,omitempty"`
	LayerBytes  []int64 `json:"layer_bytes,omitempty"`
}

type DistributedGroup struct {
	GroupID     string            `json:"group_id"`
	Model       string            `json:"model"`
	ModelDigest string            `json:"model_digest,omitempty"`
	LayerCount  int               `json:"layer_count"`
	HiddenSize  int               `json:"hidden_size,omitempty"`
	Stages      []StageAssignment `json:"stages"`
	ExpiresAt   int64             `json:"expires_at"`
}

type StageAssignment struct {
	StageIndex      int    `json:"stage_index"`
	NodeID          string `json:"node_id"`
	Endpoint        string `json:"endpoint"`
	LayerStart      int    `json:"layer_start"`
	LayerEnd        int    `json:"layer_end"`
	MemoryBudgetMB  int    `json:"memory_budget_mb,omitempty"`
	TensorBytes     int64  `json:"tensor_bytes,omitempty"`
	ReservationID   string `json:"reservation_id"`
	Token           string `json:"token"`
	NextStageNodeID string `json:"next_stage_node_id,omitempty"`
}
