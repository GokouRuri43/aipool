package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	host := flag.String("host", "", "relay DNS name or IP address")
	out := flag.String("out", "relay-certs", "output directory")
	days := flag.Int("days", 365, "certificate validity in days")
	flag.Parse()
	if *host == "" {
		log.Fatal("-host is required")
	}
	if err := os.MkdirAll(*out, 0700); err != nil {
		log.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		log.Fatal(err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		log.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: *host, Organization: []string{"AIPool Self-Hosted Relay"}}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(time.Duration(*days) * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(*host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{*host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	certPath, keyPath := filepath.Join(*out, "relay.crt"), filepath.Join(*out, "relay.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		log.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(der)
	fmt.Printf("certificate=%s\nprivate_key=%s\nserver_name=%s\nfingerprint_sha256=%s\n", certPath, keyPath, *host, hex.EncodeToString(sum[:]))
}
