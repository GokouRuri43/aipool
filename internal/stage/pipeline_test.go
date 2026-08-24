package stage

import (
	"bytes"
	"net"
	"testing"

	"github.com/local/aipool/internal/tensorwire"
)

func TestMultiStagePipelineMatchesSingleProcessLayers(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	stages := [][2]int{{0, 7}, {7, 19}, {19, 32}}
	addresses := make([]string, 0, len(stages))
	for _, layers := range stages {
		runtime, err := NewRuntime(layers[0], layers[1], 16, DeterministicBackend{})
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		addresses = append(addresses, listener.Addr().String())
		go func() { _ = (&Server{Runtime: runtime, Key: key}).Serve(listener) }()
	}
	input := make([]float32, 16)
	for i := range input {
		input[i] = float32(i) / 10
	}
	want := append([]float32(nil), input...)
	if err := (DeterministicBackend{}).Execute(0, 32, 5, want); err != nil {
		t.Fatal(err)
	}
	frame := tensorwire.Frame{SessionID: 42, Sequence: 0, Position: 5, Values: input}
	for _, address := range addresses {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		if err := tensorwire.WriteFrame(conn, key, frame); err != nil {
			t.Fatal(err)
		}
		frame, err = tensorwire.ReadFrame(conn, key)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range want {
		if frame.Values[i] != want[i] {
			t.Fatalf("distributed value %d=%v, single=%v", i, frame.Values[i], want[i])
		}
	}
}

func TestRuntimeRejectsOutOfOrderSessionFrames(t *testing.T) {
	runtime, _ := NewRuntime(0, 2, 2, DeterministicBackend{})
	if _, err := runtime.Process(tensorwire.Frame{SessionID: 1, Sequence: 1, Values: []float32{1, 2}}); err == nil {
		t.Fatal("out-of-order frame was accepted")
	}
}
