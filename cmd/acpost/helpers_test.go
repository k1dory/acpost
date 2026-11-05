package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type ctxT = context.Context

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
