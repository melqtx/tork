package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A hostile endpoint streaming an unbounded body must be capped, not OOM.
func TestFetchCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, 1<<16)
		for i := 0; i < 64; i++ { // try to send ~4 MiB
			w.Write(buf)
		}
	}))
	defer srv.Close()

	old := maxResponseBytes
	maxResponseBytes = 100 << 10 // 100 KiB cap for the test
	defer func() { maxResponseBytes = old }()

	resp, err := fetch(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	if n > maxResponseBytes {
		t.Errorf("read %d bytes, cap was %d - body not limited", n, maxResponseBytes)
	}
}
