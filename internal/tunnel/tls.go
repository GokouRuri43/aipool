package tunnel

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

func PinnedTLSConfig(serverName, fingerprint string) (*tls.Config, error) {
	fingerprint = strings.ToLower(strings.ReplaceAll(fingerprint, ":", ""))
	expected, err := hex.DecodeString(fingerprint)
	if err != nil || len(expected) != sha256.Size {
		return nil, fmt.Errorf("relay certificate fingerprint must be a SHA-256 hex string")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         serverName,
		InsecureSkipVerify: true, // Replaced by the explicit certificate pin below.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("relay did not provide a certificate")
			}
			actual := sha256.Sum256(rawCerts[0])
			if !strings.EqualFold(hex.EncodeToString(actual[:]), fingerprint) {
				return fmt.Errorf("relay certificate fingerprint mismatch")
			}
			return nil
		},
	}, nil
}
