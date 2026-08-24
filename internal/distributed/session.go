package distributed

import (
	"context"
	"fmt"
	"sync"

	"github.com/local/aipool/internal/stage"
	"github.com/local/aipool/internal/tensorwire"
)

type Session struct {
	ID     uint64
	Stages []stage.Client
	mu     sync.Mutex
	next   uint64
}

func (s *Session) Run(ctx context.Context, position uint32, activation []float32) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ID == 0 || len(s.Stages) < 2 || len(activation) == 0 {
		return nil, fmt.Errorf("distributed session requires an ID, at least two stages and an activation")
	}
	frame := tensorwire.Frame{SessionID: s.ID, Sequence: s.next, Position: position, Values: append([]float32(nil), activation...)}
	for index, client := range s.Stages {
		if client == nil {
			return nil, fmt.Errorf("stage %d is unavailable", index)
		}
		output, err := client.Process(ctx, frame)
		if err != nil {
			return nil, fmt.Errorf("stage %d failed: %w", index, err)
		}
		frame = output
	}
	s.next++
	return frame.Values, nil
}
