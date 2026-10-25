package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestYTSDoesNotReportAPIErrorAsHealthyEmptySearch(t *testing.T) {
	for _, body := range []string{`{}`, `{"status":"error","status_message":"rate limited"}`, `{"status":"ok","data":{}}`, `{"status":"ok","data":{"movie_count":1}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
		p := NewYTS(srv.Client(), srv.URL)
		p.bases = []string{srv.URL}
		err := p.Search(context.Background(), "example", make(chan Result, 1))
		srv.Close()
		if !errors.Is(err, ErrResponseFormat) {
			t.Fatalf("API error returned a healthy empty search: %v", err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok","data":{"movie_count":0}}`))
	}))
	defer srv.Close()
	if rows := collect(t, NewYTS(srv.Client(), srv.URL), "no match"); len(rows) != 0 {
		t.Fatal("valid empty response failed")
	}
}

func TestX1337SkipsUnrecognizedPageInsteadOfReportingWorking(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`<html><title>Login</title></html>`)) }))
	defer bad.Close()
	good := serveFixture(t, "1337x_search.html")
	defer good.Close()
	p := NewX1337(good.Client(), []string{bad.URL, good.URL})
	if rows := collect(t, p, "example"); len(rows) == 0 {
		t.Fatal("false empty response prevented mirror failover")
	}
	p = NewX1337(bad.Client(), []string{bad.URL})
	if err := p.Search(context.Background(), "example", make(chan Result, 1)); !errors.Is(err, ErrResponseFormat) {
		t.Fatal("unrecognized HTML was labeled a working search")
	}
}

func TestX1337UpdatesOldPublicMirrorListsWithoutLeakingCustomSearches(t *testing.T) {
	p := NewX1337(nil, []string{"https://1337x.to", "https://1337x.st", "https://x1337x.ws"})
	if len(p.mirrors) != 5 {
		t.Fatal("older defaults missed maintained public aliases")
	}
	p = NewX1337(nil, []string{"https://private.example/mirror"})
	if len(p.mirrors) != 1 || p.mirrors[0] != "https://private.example/mirror" {
		t.Fatal("custom mirror gained unconfigured public endpoints")
	}
}
