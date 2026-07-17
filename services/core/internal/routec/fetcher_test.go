package routec_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/routec"
)

// TestHTTPFetcherClassifies drives the mainline fetcher against an httptest
// server (OFFLINE — never live DK) and asserts each response is classified into
// the right breaker signal.
func TestHTTPFetcherClassifies(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    routec.Signal
	}{
		{"ok", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":200,"data":{}}`))
		}, routec.SignalOK},
		{"403", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"status":403}`))
		}, routec.Signal403},
		{"429", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"status":429}`))
		}, routec.Signal429},
		{"challenge", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`<html><body>Please complete the captcha to continue</body></html>`))
		}, routec.SignalChallenge},
		{"server_error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(503)
		}, routec.SignalTransport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			f := routec.NewHTTPFetcher(srv.Client(), 0, 1<<20)
			res, _ := f.Fetch(context.Background(), routec.FetchRequest{URL: srv.URL, Account: uuid.New()})
			if res.Signal != tc.want {
				t.Fatalf("%s: signal got %s want %s (status %d)", tc.name, res.Signal, tc.want, res.StatusCode)
			}
		})
	}
}

// TestHTTPFetcherRefusesCrossHostRedirect asserts host pinning (docs/12): a 302
// to a DIFFERENT host is NOT followed, so Route C never leaves the DK host even
// when DK redirects to a challenge/login page elsewhere.
func TestHTTPFetcherRefusesCrossHostRedirect(t *testing.T) {
	var otherHit int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&otherHit, 1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`<html>challenge</html>`))
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer origin.Close()

	f := routec.NewHTTPFetcher(origin.Client(), 0, 1<<20)
	res, err := f.Fetch(context.Background(), routec.FetchRequest{URL: origin.URL, Account: uuid.New()})
	if atomic.LoadInt32(&otherHit) != 0 {
		t.Fatal("cross-host redirect was followed (host pinning breached)")
	}
	// The refused redirect surfaces as a transport-class outcome, never a healthy
	// fetch of the other host.
	if err == nil && res.Signal == routec.SignalOK {
		t.Fatal("cross-host redirect must not produce a healthy fetch")
	}
}

// TestHTTPFetcherLatencyClassification asserts a slow 200 is classified as
// SignalLatency when it exceeds the ceiling.
func TestHTTPFetcherLatencyClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	f := routec.NewHTTPFetcher(srv.Client(), 10*time.Millisecond, 1<<20)
	res, _ := f.Fetch(context.Background(), routec.FetchRequest{URL: srv.URL, Account: uuid.New()})
	if res.Signal != routec.SignalLatency {
		t.Fatalf("slow response signal: got %s want latency", res.Signal)
	}
}
