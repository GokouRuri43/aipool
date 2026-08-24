package host

import (
	"time"

	"github.com/local/aipool/internal/auth"
)

func issueTestLease(secret, node, model string) (string, any, error) {
	token, claims, err := auth.Issue([]byte(secret), node, model, time.Minute)
	return token, claims, err
}
