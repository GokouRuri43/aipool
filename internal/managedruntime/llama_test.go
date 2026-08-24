package managedruntime

import "testing"

func TestMissingManagedRuntimeIsUnavailable(t *testing.T) {
	runtime := NewLlama(t.TempDir()+"/missing-llama-server", 0)
	if err := runtime.Available(); err == nil {
		t.Fatal("expected missing runtime to be unavailable")
	}
}
