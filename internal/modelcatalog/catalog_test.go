package modelcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raw-name.gguf")
	if err := os.WriteFile(path, []byte("model bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load("", "public-name="+path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := catalog.Lookup("public-name")
	if !ok || entry.Size != 11 || len(entry.Digest) != 64 || entry.Format != "gguf" {
		t.Fatalf("unexpected entry: %#v", entry)
	}
}
