package store

import (
	"crypto/rand"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var idMu sync.Mutex
var lastMS int64
var lastRand [10]byte

// NewID returns a new message id: "msg_" and a ULID (48 bits of milliseconds, 80 random bits, in
// Crockford base32). Ids made by one process sort in the order they were made.
func NewID() string {
	idMu.Lock()
	now := time.Now().UnixMilli()
	if now <= lastMS {
		now = lastMS
		// Same millisecond: increment the random part so the order holds.
		for i := len(lastRand) - 1; i >= 0; i-- {
			lastRand[i]++
			if lastRand[i] != 0 {
				break
			}
		}
	} else {
		lastMS = now
		_, _ = rand.Read(lastRand[:])
	}
	var b [16]byte
	for i := range 6 {
		b[i] = byte(now >> (40 - 8*i))
	}
	copy(b[6:], lastRand[:])
	idMu.Unlock()
	return "msg_" + encode(b)
}

func encode(b [16]byte) string {
	// 128 bits → 26 characters; the first character carries the top 3 bits.
	var out [26]byte
	var acc uint64
	var bits uint
	pos := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && pos >= 0 {
			out[pos] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			pos--
		}
	}
	if pos >= 0 {
		out[pos] = crockford[acc&31]
	}
	return string(out[:])
}
