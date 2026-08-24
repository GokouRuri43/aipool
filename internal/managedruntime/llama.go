package managedruntime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

type Llama struct {
	Executable  string
	Host        string
	Port        int
	GPULayers   int
	ContextSize int
	ExtraArgs   []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    <-chan error
	digest  string
	modelID string
	client  *http.Client
}

func NewLlama(executable string, port int) *Llama {
	if port == 0 {
		port = 18081
	}
	return &Llama{
		Executable: executable, Host: "127.0.0.1", Port: port, GPULayers: 99, ContextSize: 4096,
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

func (l *Llama) Available() error {
	if l.Executable == "" {
		return fmt.Errorf("managed llama-server executable is not configured")
	}
	info, err := os.Stat(l.Executable)
	if err != nil {
		return fmt.Errorf("managed llama-server not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("managed llama-server path is a directory")
	}
	return nil
}

func (l *Llama) Ensure(ctx context.Context, modelID, digest, modelPath string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	baseURL := "http://" + net.JoinHostPort(l.Host, strconv.Itoa(l.Port))
	if l.cmd != nil && l.digest == digest && l.healthy(ctx, baseURL) {
		return baseURL, nil
	}
	l.stopLocked()
	if err := l.Available(); err != nil {
		return "", err
	}
	args := []string{
		"--model", modelPath, "--host", l.Host, "--port", strconv.Itoa(l.Port),
		"--alias", modelID, "--n-gpu-layers", strconv.Itoa(l.GPULayers),
		"--ctx-size", strconv.Itoa(l.ContextSize), "--parallel", "1",
	}
	args = append(args, l.ExtraArgs...)
	cmd := exec.Command(l.Executable, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	l.cmd, l.done, l.digest, l.modelID = cmd, done, digest, modelID
	go func() { done <- cmd.Wait() }()

	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if l.healthy(ctx, baseURL) {
			return baseURL, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-done:
			return "", fmt.Errorf("managed llama-server exited while loading model: %v", err)
		case <-deadline.C:
			l.stopLocked()
			return "", fmt.Errorf("managed llama-server model load timed out")
		case <-ticker.C:
		}
	}
}

func (l *Llama) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopLocked()
	return nil
}

func (l *Llama) healthy(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}

func (l *Llama) stopLocked() {
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
		if l.done != nil {
			select {
			case <-l.done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	l.cmd, l.done, l.digest, l.modelID = nil, nil, "", ""
}
