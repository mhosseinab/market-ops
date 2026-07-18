package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// healthBody mirrors the generated Health schema shape for decode assertions.
type healthBody struct {
	Status string `json:"status"`
	Build  struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"buildTime"`
	} `json:"build"`
}

func TestHealthz(t *testing.T) {
	info := BuildInfo{Version: "test", Commit: "abc123", BuildTime: "2026-07-17T00:00:00Z"}
	srv := NewServer(":0", info, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantOK     bool
	}{
		{"get healthz", http.MethodGet, "/healthz", http.StatusOK, true},
		{"post healthz not allowed", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, false},
		{"unknown path", http.MethodGet, "/nope", http.StatusNotFound, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !tc.wantOK {
				return
			}

			var got healthBody
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got.Status != "ok" {
				t.Errorf("status field = %q, want %q", got.Status, "ok")
			}
			if got.Build.Version != info.Version ||
				got.Build.Commit != info.Commit ||
				got.Build.BuildTime != info.BuildTime {
				t.Errorf("build = %+v, want %+v", got.Build, info)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}
}
