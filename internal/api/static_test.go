package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func spaTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/", UIHandler())
	return mux
}

func TestSPAUnknownPathFallsBackToIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/leads/42", nil)

	spaTestMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "<") {
		t.Fatalf("body does not look like HTML: %q", rec.Body.String())
	}
}

func TestSPAUnknownAPIReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)

	spaTestMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("API 404 must not return index.html, got: %q", rec.Body.String())
	}
}

func TestSPAStaticAssetServed(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := StaticHandler(fsys)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestSPARejectsNonGet(t *testing.T) {
	h := StaticHandler(fstest.MapFS{"index.html": {Data: []byte("x")}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/anything", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
