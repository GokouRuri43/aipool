package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/local/aipool/internal/distributed"
	"github.com/local/aipool/internal/gguf"
)

func main() {
	model := required("AIPOOL_DISTRIBUTED_MODEL")
	metadata, err := gguf.ReadFile(model)
	if err != nil {
		log.Fatal(err)
	}
	runtime := &distributed.LlamaRPC{Executable: required("AIPOOL_LLAMA_SERVER"), ModelPath: model, RPCServers: split(required("AIPOOL_RPC_SERVERS"))}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer runtime.Close()
	url, err := runtime.Start(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("distributed llama.cpp ready at %s; architecture=%s layers=%d hidden=%d RPC workers=%d", url, metadata.Architecture, metadata.LayerCount, metadata.EmbeddingLength, len(runtime.RPCServers))
	client := &http.Client{Timeout: 10 * time.Second}
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/health", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}
func split(value string) []string {
	parts := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			parts = append(parts, item)
		}
	}
	return parts
}
func required(key string) string {
	value := os.Getenv(key)
	if value == "" {
		data, _ := json.Marshal(key)
		log.Fatalf("required environment variable %s is missing", data)
	}
	return value
}
