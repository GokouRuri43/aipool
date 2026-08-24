package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/local/aipool/internal/gguf"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: ggufinspect <model.gguf>")
	}
	metadata, err := gguf.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	output := struct {
		Version         uint32  `json:"version"`
		Architecture    string  `json:"architecture"`
		TensorCount     uint64  `json:"tensor_count"`
		LayerCount      int     `json:"layer_count"`
		EmbeddingLength int     `json:"embedding_length"`
		ContextLength   int     `json:"context_length"`
		FileSize        int64   `json:"file_size"`
		TensorBytes     int64   `json:"tensor_bytes"`
		NonLayerBytes   int64   `json:"non_layer_bytes"`
		LayerBytes      []int64 `json:"layer_bytes"`
	}{metadata.Version, metadata.Architecture, metadata.TensorCount, metadata.LayerCount, metadata.EmbeddingLength, metadata.ContextLength, metadata.FileSize, metadata.TensorBytes, metadata.NonLayerBytes, metadata.LayerBytes}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		log.Fatal(err)
	}
}
