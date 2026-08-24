package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/local/aipool/internal/tunnel"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "pair-id" {
		if !tunnel.ValidPairToken(os.Args[2]) {
			log.Fatal("pair token must contain at least 32 base64url bytes")
		}
		fmt.Println(tunnel.PairIDFromToken(os.Args[2]))
		return
	}
	role := required("AIPOOL_TUNNEL_ROLE")
	tlsConfig, err := tunnel.PinnedTLSConfig(required("AIPOOL_RELAY_SERVER_NAME"), required("AIPOOL_RELAY_FINGERPRINT"))
	if err != nil {
		log.Fatal(err)
	}
	cfg := tunnel.AgentConfig{
		Role: role, PairID: required("AIPOOL_PAIR_ID"), RelayAddress: required("AIPOOL_RELAY_ADDRESS"),
		RelayToken: required("AIPOOL_PAIR_TOKEN"), TunnelKey: required("AIPOOL_TUNNEL_KEY"), TLSConfig: tlsConfig,
		Forwards: parseMappings(os.Getenv("AIPOOL_TUNNEL_FORWARDS")), Targets: parseMappings(os.Getenv("AIPOOL_TUNNEL_TARGETS")),
	}
	agent, err := tunnel.NewAgent(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("%s tunnel connecting to %s for pair %s", role, cfg.RelayAddress, cfg.PairID)
	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func parseMappings(value string) map[string]string {
	result := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, target, ok := strings.Cut(item, "=")
		if !ok || key == "" || target == "" {
			log.Fatalf("invalid tunnel mapping %q", item)
		}
		result[key] = target
	}
	return result
}
func required(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
