package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/local/aipool/internal/api"
)

var raw = base64.RawURLEncoding

func Issue(secret []byte, nodeID, model string, ttl time.Duration) (string, api.LeaseClaims, error) {
	return IssueModel(secret, nodeID, model, "", 0, ttl)
}

func IssueModel(secret []byte, nodeID, model, digest string, size int64, ttl time.Duration) (string, api.LeaseClaims, error) {
	return issueClaims(secret, api.LeaseClaims{NodeID: nodeID, Model: model, ModelDigest: digest, ModelSize: size, ExpiresAt: time.Now().Add(ttl).Unix()})
}

func IssueStage(secret []byte, nodeID, model, digest string, size int64, groupID string, stageIndex, layerStart, layerEnd int, ttl time.Duration) (string, api.LeaseClaims, error) {
	if groupID == "" || stageIndex < 0 || layerStart < 0 || layerEnd <= layerStart {
		return "", api.LeaseClaims{}, errors.New("invalid distributed stage claims")
	}
	return issueClaims(secret, api.LeaseClaims{NodeID: nodeID, Model: model, ModelDigest: digest, ModelSize: size, GroupID: groupID, StageIndex: stageIndex, LayerStart: layerStart, LayerEnd: layerEnd, ExpiresAt: time.Now().Add(ttl).Unix()})
}

func issueClaims(secret []byte, claims api.LeaseClaims) (string, api.LeaseClaims, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", api.LeaseClaims{}, err
	}
	claims.Nonce = raw.EncodeToString(nonce)
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", api.LeaseClaims{}, err
	}
	body := raw.EncodeToString(payload)
	sig := sign(secret, body)
	return body + "." + raw.EncodeToString(sig), claims, nil
}

func Verify(secret []byte, token string) (api.LeaseClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return api.LeaseClaims{}, errors.New("invalid lease token")
	}
	sig, err := raw.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, sign(secret, parts[0])) {
		return api.LeaseClaims{}, errors.New("invalid lease signature")
	}
	payload, err := raw.DecodeString(parts[0])
	if err != nil {
		return api.LeaseClaims{}, errors.New("invalid lease payload")
	}
	var claims api.LeaseClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return api.LeaseClaims{}, errors.New("invalid lease claims")
	}
	if claims.ExpiresAt <= time.Now().Unix() {
		return api.LeaseClaims{}, errors.New("lease expired")
	}
	return claims, nil
}

func sign(secret []byte, body string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(body))
	return h.Sum(nil)
}

func EqualSecret(got, expected string) bool {
	return hmac.Equal([]byte(got), []byte(expected))
}
