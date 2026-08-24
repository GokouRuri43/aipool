package distributed

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/local/aipool/internal/stage"
	"github.com/local/aipool/internal/tensorwire"
)

func TestSessionRunsEveryStageForMultipleDecodePositions(t *testing.T) {
	first, _ := stage.NewRuntime(0, 5, 4, stage.DeterministicBackend{})
	second, _ := stage.NewRuntime(5, 12, 4, stage.DeterministicBackend{})
	session := Session{ID: 99, Stages: []stage.Client{stage.LocalClient{Runtime: first}, stage.LocalClient{Runtime: second}}}
	for position := uint32(0); position < 3; position++ {
		input := []float32{1, 2, 3, 4}
		got, err := session.Run(context.Background(), position, input)
		if err != nil {
			t.Fatal(err)
		}
		want := append([]float32(nil), input...)
		_ = (stage.DeterministicBackend{}).Execute(0, 12, position, want)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("position %d value %d mismatch", position, i)
			}
		}
	}
}

type failingStage struct{}

func (failingStage) Process(context.Context, tensorwire.Frame) (tensorwire.Frame, error) {
	return tensorwire.Frame{}, errors.New("offline")
}

func TestSessionReportsFailedStage(t *testing.T) {
	runtime, _ := stage.NewRuntime(0, 1, 2, stage.DeterministicBackend{})
	session := Session{ID: 1, Stages: []stage.Client{stage.LocalClient{Runtime: runtime}, failingStage{}}}
	if _, err := session.Run(context.Background(), 0, []float32{1, 2}); err == nil || !strings.Contains(err.Error(), "stage 1 failed") {
		t.Fatalf("unexpected failure: %v", err)
	}
}
