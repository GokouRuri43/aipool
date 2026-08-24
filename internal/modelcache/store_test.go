package modelcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestResumeAndVerify(t *testing.T) {
	data := "remote model bytes"
	sum := sha256.Sum256([]byte(data))
	digest := hex.EncodeToString(sum[:])
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Append(digest, int64(len(data)), 0, strings.NewReader(data[:7]), 8)
	if err != nil || status.Received != 7 || status.Ready {
		t.Fatalf("unexpected first chunk: %#v %v", status, err)
	}
	status, err = store.Append(digest, int64(len(data)), 7, strings.NewReader(data[7:]), 64)
	if err != nil || !status.Ready {
		t.Fatalf("unexpected completion: %#v %v", status, err)
	}
	if _, err := store.Append(digest, int64(len(data)), 3, strings.NewReader("x"), 8); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsWrongOffsetAndDigest(t *testing.T) {
	data := "abc"
	sum := sha256.Sum256([]byte(data))
	digest := hex.EncodeToString(sum[:])
	store, _ := New(t.TempDir())
	_, err := store.Append(digest, 3, 1, strings.NewReader(data), 8)
	var offsetErr *OffsetError
	if !errors.As(err, &offsetErr) || offsetErr.Expected != 0 {
		t.Fatalf("expected offset error, got %v", err)
	}
	wrong := strings.Repeat("0", 64)
	if _, err := store.Append(wrong, 3, 0, strings.NewReader(data), 8); err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestRejectsEmptyChunk(t *testing.T) {
	digest := strings.Repeat("0", 64)
	store, _ := New(t.TempDir())
	if _, err := store.Append(digest, 3, 0, strings.NewReader(""), 8); err == nil {
		t.Fatal("expected empty chunk to be rejected")
	}
}

func TestStatusRecoversCompletePartFile(t *testing.T) {
	data := []byte("complete model")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	store, _ := New(t.TempDir())
	if err := os.WriteFile(store.partPath(digest), data, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(digest, int64(len(data)))
	if err != nil || !status.Ready {
		t.Fatalf("complete part was not recovered: %#v %v", status, err)
	}
}
