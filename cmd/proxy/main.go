package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/local/aipool/internal/distributed"
	"github.com/local/aipool/internal/modelcatalog"
	"github.com/local/aipool/internal/proxy"
)

func main() {
	addr := env("AIPOOL_PROXY_ADDR", "127.0.0.1:11434")
	shared := env("AIPOOL_SHARED_SECRET", "dev-only-change-me")
	var catalog *modelcatalog.Catalog
	modelDir, modelMappings := os.Getenv("AIPOOL_LOCAL_MODEL_DIR"), os.Getenv("AIPOOL_LOCAL_MODELS")
	if modelDir != "" || modelMappings != "" {
		var err error
		catalog, err = modelcatalog.Load(modelDir, modelMappings)
		if err != nil {
			log.Fatalf("could not load local model catalog: %v", err)
		}
	}
	controlURL := env("AIPOOL_CONTROL_URL", "http://127.0.0.1:8080")
	clientSecret := env("AIPOOL_CLIENT_SECRET", shared)
	var distributedManager *distributed.Manager
	if executable := os.Getenv("AIPOOL_DISTRIBUTED_LLAMA_SERVER"); executable != "" {
		distributedManager = distributed.NewManager(distributed.ManagerConfig{ControlURL: controlURL, ClientSecret: clientSecret, Executable: executable, MinNodes: envInt("AIPOOL_DISTRIBUTED_MIN_NODES", 2), MaxNodes: envInt("AIPOOL_DISTRIBUTED_MAX_NODES", 2), Port: envInt("AIPOOL_DISTRIBUTED_PORT", 18082)})
		defer distributedManager.Close()
	}
	server := proxy.NewWithConfig(proxy.Config{ControlURL: controlURL, ClientSecret: clientSecret, Catalog: catalog, Distributed: distributedManager, DistributedDefault: os.Getenv("AIPOOL_DISTRIBUTED_DEFAULT") == "1"})
	if catalog != nil {
		log.Printf("loaded %d local model(s)", catalog.Len())
	}
	log.Printf("local OpenAI proxy listening on http://%s", addr)
	httpServer := &http.Server{Addr: addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	log.Fatal(httpServer.ListenAndServe())
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Fatalf("%s must be a positive integer", key)
	}
	return parsed
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
