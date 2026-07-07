package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The apibay backend can treat '+' literally, so spaces must go out as %20.
func TestPirateBayEncodesSpacesAsPercent20(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	out := make(chan Result, 4)
	if err := NewPirateBayTV(srv.Client(), srv.URL).Search(context.Background(), "breaking bad", out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawQuery, "q=breaking%20bad") {
		t.Errorf("raw query = %q, want it to contain q=breaking%%20bad (not '+')", rawQuery)
	}
	if strings.Contains(rawQuery, "+") {
		t.Errorf("raw query %q still uses '+' for spaces", rawQuery)
	}
}

// A dead primary mirror must fail over to the next one.
func TestPirateBayMirrorFailover(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	good := serveFixture(t, "apibay.json")

	p := NewPirateBayMovies(nil, dead.URL, good.URL)
	results := collect(t, p, "ubuntu")
	if len(results) != 1 {
		t.Fatalf("failover should have produced 1 result, got %d", len(results))
	}
}
