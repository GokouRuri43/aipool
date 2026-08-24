package auth

import (
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	token, issued, err := Issue([]byte("secret"), "node-1", "model-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify([]byte("secret"), token)
	if err != nil {
		t.Fatal(err)
	}
	if verified != issued {
		t.Fatalf("claims mismatch: %#v != %#v", verified, issued)
	}
	if _, err := Verify([]byte("wrong"), token); err == nil {
		t.Fatal("expected signature error")
	}
}

func TestExpiredToken(t *testing.T) {
	token, _, err := Issue([]byte("secret"), "node-1", "model-1", -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify([]byte("secret"), token); err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestModelLeaseBindsDigestAndSize(t *testing.T) {
	token, issued, err := IssueModel([]byte("secret"), "node", "model", "abc123", 42, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify([]byte("secret"), token)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ModelDigest != "abc123" || verified.ModelDigest != "abc123" || verified.ModelSize != 42 {
		t.Fatalf("model metadata was not bound into lease: %#v", verified)
	}
}

func TestStageLeaseBindsGroupAndLayerRange(t *testing.T) {
	token, _, err := IssueStage([]byte("secret"), "node", "model", "", 0, "group", 1, 10, 20, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify([]byte("secret"), token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.GroupID != "group" || claims.StageIndex != 1 || claims.LayerStart != 10 || claims.LayerEnd != 20 {
		t.Fatalf("stage claims mismatch: %#v", claims)
	}
}
