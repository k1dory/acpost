package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/node"
)

// slowWriter is a client that takes its time reading: the first write waits for release.
type slowWriter struct {
	mu      sync.Mutex
	h       http.Header
	body    bytes.Buffer
	code    int
	release chan struct{}
	once    sync.Once
}

func (w *slowWriter) Header() http.Header { return w.h }
func (w *slowWriter) WriteHeader(c int)   { w.code = c }
func (w *slowWriter) Flush()              {}
func (w *slowWriter) Write(b []byte) (int, error) {
	w.once.Do(func() { <-w.release })
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(b)
}

// An error before the stream started is the whole answer, however slowly it is written: a
// heartbeat that comes due meanwhile adds nothing after it.
func TestEarlyErrorIsTheWholeAnswer(t *testing.T) {
	old := ssePing
	ssePing = 10 * time.Millisecond
	t.Cleanup(func() { ssePing = old })
	w := &slowWriter{h: http.Header{}, release: make(chan struct{})}
	st, _ := newSSE(w, httptest.NewRequest("GET", "/", nil))
	failed := make(chan struct{})
	go func() {
		st.fail(&node.Error{Code: "not_found", Message: "no terminal x"})
		close(failed)
	}()
	time.Sleep(100 * time.Millisecond) // heartbeats come due while the error is being written
	close(w.release)
	<-failed
	time.Sleep(50 * time.Millisecond)
	st.done()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code != 404 || bytes.Contains(w.body.Bytes(), []byte("ping")) || errCode(w.body.String()) != "not_found" {
		t.Fatalf("%d %q", w.code, w.body.String())
	}
}
