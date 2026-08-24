package tunnel

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const maxHandshakeSize = 16 << 10

type handshake struct {
	Type     string `json:"type"`
	Role     string `json:"role"`
	PairID   string `json:"pair_id"`
	StreamID string `json:"stream_id,omitempty"`
	Target   string `json:"target,omitempty"`
}

type authChallenge struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce"`
}

type authResponse struct {
	Type      string `json:"type"`
	Signature string `json:"signature"`
}

type controlMessage struct {
	Type     string `json:"type"`
	StreamID string `json:"stream_id,omitempty"`
	Target   string `json:"target,omitempty"`
	Error    string `json:"error,omitempty"`
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxHandshakeSize {
		return fmt.Errorf("tunnel message is too large")
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func readJSONLine(reader *bufio.Reader, value any) error {
	data, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	if len(data) > maxHandshakeSize {
		return fmt.Errorf("tunnel message is too large")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid tunnel message: %w", err)
	}
	return nil
}

func peerRole(role string) string {
	if role == "requester" {
		return "provider"
	}
	if role == "provider" {
		return "requester"
	}
	return ""
}

func PairIDFromToken(token string) string {
	privateKey := pairPrivateKey(token)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return hex.EncodeToString(publicKey)
}

func ValidPairToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) >= 32
}

func pairPrivateKey(token string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("aipool-relay-auth-v2|" + token))
	return ed25519.NewKeyFromSeed(seed[:])
}

func authPayload(hello handshake, nonce string) []byte {
	return []byte("aipool-relay-auth-v2|" + hello.Type + "|" + hello.Role + "|" + hello.PairID + "|" + hello.StreamID + "|" + hello.Target + "|" + nonce)
}

func signChallenge(token string, hello handshake, nonce string) string {
	signature := ed25519.Sign(pairPrivateKey(token), authPayload(hello, nonce))
	return base64.RawURLEncoding.EncodeToString(signature)
}

func verifyChallenge(hello handshake, nonce, encodedSignature string) bool {
	publicKey, keyErr := hex.DecodeString(hello.PairID)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(encodedSignature)
	return keyErr == nil && len(publicKey) == ed25519.PublicKeySize && signatureErr == nil &&
		ed25519.Verify(ed25519.PublicKey(publicKey), authPayload(hello, nonce), signature)
}
