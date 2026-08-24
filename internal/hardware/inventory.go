package hardware

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/local/aipool/internal/api"
)

func Detect(ctx context.Context) api.HardwareInventory {
	totalMemory, freeMemory := detectMemoryMB()
	result := api.HardwareInventory{OS: runtime.GOOS, Arch: runtime.GOARCH, CPULogical: runtime.NumCPU(), MemoryMB: totalMemory, MemoryFreeMB: freeMemory, GPUDevices: []api.GPUDevice{}}
	result.GPUDevices = detectNVIDIA(ctx)
	return result
}

func DetectWithTimeout() api.HardwareInventory {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return Detect(ctx)
}

func detectNVIDIA(ctx context.Context) []api.GPUDevice {
	cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,name,uuid,memory.total,memory.free,driver_version,compute_cap", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return []api.GPUDevice{}
	}
	devices, err := ParseNvidiaSMI(output)
	if err != nil {
		return []api.GPUDevice{}
	}
	return devices
}

func ParseNvidiaSMI(output []byte) ([]api.GPUDevice, error) {
	devices := []api.GPUDevice{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 7 {
			return nil, fmt.Errorf("unexpected nvidia-smi row: %q", line)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, err
		}
		total, err := strconv.Atoi(parts[3])
		if err != nil {
			return nil, err
		}
		free, err := strconv.Atoi(parts[4])
		if err != nil {
			return nil, err
		}
		devices = append(devices, api.GPUDevice{Index: index, Name: parts[1], UUID: parts[2], MemoryTotalMB: total, MemoryFreeMB: free, DriverVersion: parts[5], ComputeCapability: parts[6]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}
