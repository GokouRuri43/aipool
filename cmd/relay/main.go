package main

import (
	"crypto/tls"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/local/aipool/internal/tunnel"
)

func main() {
	addr := env("AIPOOL_RELAY_ADDR", "0.0.0.0:8443")
	certPath, keyPath := os.Getenv("AIPOOL_RELAY_CERT"), os.Getenv("AIPOOL_RELAY_KEY")
	if certPath == "" || keyPath == "" {
		log.Fatal("AIPOOL_RELAY_CERT and AIPOOL_RELAY_KEY are required")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatalf("load relay TLS certificate: %v", err)
	}
	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	listener := tls.NewListener(tcpKeepAliveListener{tcpListener}, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13})
	defer listener.Close()
	log.Printf("AIPool relay listening on %s with TLS 1.3", addr)
	server := tunnel.NewRelayWithConfig(tunnel.RelayConfig{
		MaxConnections:        envInt("AIPOOL_RELAY_MAX_CONNECTIONS", 4096),
		MaxConnectionsPerIP:   envInt("AIPOOL_RELAY_MAX_CONNECTIONS_PER_IP", 256),
		MaxConnectionsPerPair: envInt("AIPOOL_RELAY_MAX_CONNECTIONS_PER_PAIR", 512),
		MaxPendingPerPair:     envInt("AIPOOL_RELAY_MAX_PENDING_PER_PAIR", 128),
	})
	go func() {
		for range time.Tick(time.Minute) {
			log.Printf("relay status: %s", server.Stats())
		}
	}()
	log.Fatal(server.Serve(tcpKeepAliveListener{listener}))
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

type tcpKeepAliveListener struct{ net.Listener }

func (l tcpKeepAliveListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}
	return conn, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
