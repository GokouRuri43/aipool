package main

import (
	"encoding/base64"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/local/aipool/internal/stage"
)

func main() {
	address := env("AIPOOL_STAGE_ADDR", "127.0.0.1:18100")
	key, err := base64.RawURLEncoding.DecodeString(required("AIPOOL_STAGE_KEY"))
	if err != nil || len(key) < 32 {
		log.Fatal("AIPOOL_STAGE_KEY must contain at least 32 base64url bytes")
	}
	runtime, err := stage.NewRuntime(envInt("AIPOOL_STAGE_LAYER_START", -1), envInt("AIPOOL_STAGE_LAYER_END", -1), envInt("AIPOOL_STAGE_HIDDEN_SIZE", -1), stage.DeterministicBackend{})
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	log.Printf("experimental deterministic stage listening on %s for layers [%d,%d)", address, runtime.LayerStart, runtime.LayerEnd)
	log.Fatal((&stage.Server{Runtime: runtime, Key: key}).Serve(listener))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func required(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("%s must be an integer", key)
	}
	return parsed
}
