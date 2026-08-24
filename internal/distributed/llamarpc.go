package distributed

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LlamaRPC struct {
	Executable string
	ModelPath  string
	Host       string
	Port       int
	Context    int
	GPULayers  int
	RPCServers []string
	ExtraArgs  []string
	Stdout     io.Writer
	Stderr     io.Writer

	mu   sync.Mutex
	cmd  *exec.Cmd
	done <-chan error
}

func (l *LlamaRPC) Available() error {
	if l.Executable == "" {
		return fmt.Errorf("llama-server executable is required")
	}
	if info, err := os.Stat(l.Executable); err != nil || info.IsDir() {
		return fmt.Errorf("llama-server is unavailable")
	}
	if l.ModelPath == "" {
		return fmt.Errorf("GGUF model path is required")
	}
	if info, err := os.Stat(l.ModelPath); err != nil || info.IsDir() {
		return fmt.Errorf("GGUF model is unavailable")
	}
	if len(l.RPCServers) < 2 {
		return fmt.Errorf("distributed inference requires at least two RPC workers")
	}
	return nil
}

func (l *LlamaRPC) Start(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.Available(); err != nil {
		return "", err
	}
	if l.Host == "" {
		l.Host = "127.0.0.1"
	}
	if l.Port == 0 {
		l.Port = 18082
	}
	if l.Context <= 0 {
		l.Context = 4096
	}
	if l.GPULayers == 0 {
		l.GPULayers = 999
	}
	args := []string{"--model", l.ModelPath, "--host", l.Host, "--port", strconv.Itoa(l.Port), "--ctx-size", strconv.Itoa(l.Context), "--n-gpu-layers", strconv.Itoa(l.GPULayers), "--rpc", strings.Join(l.RPCServers, ",")}
	args = append(args, l.ExtraArgs...)
	command := exec.Command(l.Executable, args...)
	stdout, stderr := l.Stdout, l.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	l.cmd, l.done = command, done
	go func() { done <- command.Wait() }()
	address := net.JoinHostPort(l.Host, strconv.Itoa(l.Port))
	healthURL := "http://" + address + "/health"
	healthClient := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err == nil {
			if resp, requestErr := healthClient.Do(req); requestErr == nil {
				resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					return "http://" + address, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			l.stopLocked()
			return "", ctx.Err()
		case err := <-done:
			return "", fmt.Errorf("distributed llama-server exited: %v", err)
		case <-deadline.C:
			l.stopLocked()
			return "", fmt.Errorf("distributed llama-server startup timed out")
		case <-ticker.C:
		}
	}
}

func (l *LlamaRPC) Close() error { l.mu.Lock(); defer l.mu.Unlock(); l.stopLocked(); return nil }
func (l *LlamaRPC) stopLocked() {
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
		if l.done != nil {
			select {
			case <-l.done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	l.cmd, l.done = nil, nil
}
