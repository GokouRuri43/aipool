package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/local/aipool/internal/control"
)

func main() {
	addr := env("AIPOOL_CONTROL_ADDR", "127.0.0.1:8080")
	shared := env("AIPOOL_SHARED_SECRET", "dev-only-change-me")
	credentials := map[string]control.NodeCredential(nil)
	clientSecret := env("AIPOOL_CLIENT_SECRET", shared)
	if configPath := os.Getenv("AIPOOL_CONTROL_CONFIG"); configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("read control config: %v", err)
		}
		var config struct {
			ClientSecret string `json:"client_secret"`
			Providers    []struct {
				NodeID      string `json:"node_id"`
				HostSecret  string `json:"host_secret"`
				LeaseSecret string `json:"lease_secret"`
				Scope       string `json:"scope,omitempty"`
			} `json:"providers"`
		}
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		if err := json.Unmarshal(data, &config); err != nil {
			log.Fatalf("parse control config: %v", err)
		}
		if config.ClientSecret == "" {
			log.Fatal("control config client_secret is required")
		}
		clientSecret = config.ClientSecret
		credentials = make(map[string]control.NodeCredential, len(config.Providers))
		for _, provider := range config.Providers {
			if provider.NodeID == "" || provider.HostSecret == "" || provider.LeaseSecret == "" {
				log.Fatal("every configured provider requires node_id, host_secret and lease_secret")
			}
			if _, exists := credentials[provider.NodeID]; exists {
				log.Fatalf("duplicate provider node_id %q", provider.NodeID)
			}
			if provider.Scope == "" {
				provider.Scope = "remote"
			}
			if provider.Scope != "local" && provider.Scope != "remote" {
				log.Fatalf("provider %q has invalid scope", provider.NodeID)
			}
			credentials[provider.NodeID] = control.NodeCredential{RegistrationSecret: provider.HostSecret, LeaseSecret: provider.LeaseSecret, Scope: provider.Scope}
		}
	}
	server := control.NewWithNodeCredentials(
		env("AIPOOL_HOST_SECRET", shared),
		clientSecret,
		env("AIPOOL_LEASE_SECRET", shared),
		5*time.Minute,
		credentials,
	)
	log.Printf("control plane listening on http://%s", addr)
	httpServer := &http.Server{Addr: addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second}
	log.Fatal(httpServer.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
