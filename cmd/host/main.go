package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/local/aipool/internal/host"
	"github.com/local/aipool/internal/managedruntime"
	"github.com/local/aipool/internal/modelcache"
	"github.com/local/aipool/internal/networking"
)

func main() {
	addr := env("AIPOOL_HOST_ADDR", "0.0.0.0:8091")
	shared := env("AIPOOL_SHARED_SECRET", "dev-only-change-me")
	endpoint := os.Getenv("AIPOOL_HOST_ENDPOINT")
	if endpoint == "" {
		var err error
		endpoint, err = networking.PublicEndpoint(addr)
		if err != nil {
			log.Fatalf("could not discover LAN endpoint; set AIPOOL_HOST_ENDPOINT explicitly: %v", err)
		}
	}
	var store *modelcache.Store
	var runtimeManager host.ManagedRuntime
	cacheDir := os.Getenv("AIPOOL_MODEL_CACHE_DIR")
	if cacheDir != "" {
		var err error
		store, err = modelcache.New(cacheDir)
		if err != nil {
			log.Fatalf("could not initialize model cache: %v", err)
		}
		runtimePath := os.Getenv("AIPOOL_MANAGED_RUNTIME")
		if runtimePath == "" {
			runtimePath = filepath.Join(filepath.Dir(os.Args[0]), "runtime", "llama-server.exe")
		}
		runtimeManager = managedruntime.NewLlama(runtimePath, 18081)
	}
	server := host.New(host.Config{
		NodeID: env("AIPOOL_NODE_ID", "friend-gpu-1"), Endpoint: endpoint,
		ControlURL:         env("AIPOOL_CONTROL_URL", "http://127.0.0.1:8080"),
		RegistrationSecret: env("AIPOOL_HOST_SECRET", shared), LeaseSecret: env("AIPOOL_LEASE_SECRET", shared),
		Models: configuredModels(store == nil), RuntimeURL: os.Getenv("AIPOOL_RUNTIME_URL"),
		RuntimeKind: os.Getenv("AIPOOL_RUNTIME_KIND"), RuntimeModel: os.Getenv("AIPOOL_RUNTIME_MODEL"),
		ModelStore: store, RuntimeManager: runtimeManager,
		Scope: env("AIPOOL_NODE_SCOPE", "remote"), MaxConcurrency: envInt("AIPOOL_MAX_CONCURRENCY", 1),
		DistributedReady: os.Getenv("AIPOOL_STAGE_ENDPOINT") != "", StageEndpoint: os.Getenv("AIPOOL_STAGE_ENDPOINT"),
	})
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := server.Register(ctx)
			if err != nil {
				log.Printf("registration failed: %v", err)
			}
			cancel()
			if err != nil {
				time.Sleep(2 * time.Second)
			} else {
				time.Sleep(30 * time.Second)
			}
		}
	}()
	log.Printf("host agent listening on http://%s; advertising %s", addr, endpoint)
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

func configuredModels(mockDefault bool) []string {
	if value := strings.TrimSpace(os.Getenv("AIPOOL_MODELS")); value != "" {
		return strings.Split(value, ",")
	}
	if mockDefault {
		return []string{"mock-llm"}
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
