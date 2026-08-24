package stage

import (
	"context"
	"fmt"
	"sync"

	"github.com/local/aipool/internal/tensorwire"
)

type Backend interface {
	Execute(layerStart, layerEnd int, position uint32, values []float32) error
}

type Runtime struct {
	LayerStart int
	LayerEnd   int
	HiddenSize int
	Backend    Backend

	mu       sync.Mutex
	sequence map[uint64]uint64
}

type Client interface {
	Process(context.Context, tensorwire.Frame) (tensorwire.Frame, error)
}

func NewRuntime(layerStart, layerEnd, hiddenSize int, backend Backend) (*Runtime, error) {
	if layerStart < 0 || layerEnd <= layerStart || hiddenSize <= 0 || backend == nil {
		return nil, fmt.Errorf("valid layer range, hidden size and backend are required")
	}
	return &Runtime{LayerStart: layerStart, LayerEnd: layerEnd, HiddenSize: hiddenSize, Backend: backend, sequence: map[uint64]uint64{}}, nil
}

type LocalClient struct{ Runtime *Runtime }

func (c LocalClient) Process(_ context.Context, frame tensorwire.Frame) (tensorwire.Frame, error) {
	if c.Runtime == nil {
		return tensorwire.Frame{}, fmt.Errorf("local stage runtime is required")
	}
	return c.Runtime.Process(frame)
}

func (r *Runtime) Process(frame tensorwire.Frame) (tensorwire.Frame, error) {
	if len(frame.Values) != r.HiddenSize {
		return tensorwire.Frame{}, fmt.Errorf("activation width %d does not match stage width %d", len(frame.Values), r.HiddenSize)
	}
	r.mu.Lock()
	expected := r.sequence[frame.SessionID]
	if frame.Sequence != expected {
		r.mu.Unlock()
		return tensorwire.Frame{}, fmt.Errorf("out-of-order frame: expected sequence %d", expected)
	}
	values := append([]float32(nil), frame.Values...)
	if err := r.Backend.Execute(r.LayerStart, r.LayerEnd, frame.Position, values); err != nil {
		r.mu.Unlock()
		return tensorwire.Frame{}, err
	}
	r.sequence[frame.SessionID] = expected + 1
	r.mu.Unlock()
	frame.Values = values
	return frame, nil
}

// DeterministicBackend is a correctness harness, not an LLM implementation.
// Each layer applies a deterministic affine transform, allowing distributed
// stage results to be compared bit-for-bit with a single-process execution.
type DeterministicBackend struct{}

func (DeterministicBackend) Execute(start, end int, position uint32, values []float32) error {
	for layer := start; layer < end; layer++ {
		bias := float32(layer+1)*0.001 + float32(position)*0.00001
		for i := range values {
			values[i] = values[i]*1.0001 + bias + float32(i)*0.000001
		}
	}
	return nil
}
