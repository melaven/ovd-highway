package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestCORSPreflightBypassesAuth(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := CORS(BearerAuth("secret")(next))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("preflight request reached the protected handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, m) {
			t.Errorf("Access-Control-Allow-Methods %q missing %s", methods, m)
		}
	}
	headers := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "Authorization") || !strings.Contains(headers, "Content-Type") {
		t.Errorf("Access-Control-Allow-Headers %q missing expected values", headers)
	}
}

func TestBearerAuthRejectsMissingAndWrongToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := BearerAuth("secret")(next)

	for _, tc := range []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"wrong", "Bearer wrong"},
		{"wrong scheme", "Basic secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if body := rec.Body.String(); !strings.Contains(body, "unauthorized") {
				t.Errorf("body = %q, want JSON error", body)
			}
		})
	}
}

func TestBearerAuthAcceptsValidToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := BearerAuth("secret")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestBearerAuthEmptyTokenBypasses(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := BearerAuth("")(next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sources", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (dev-mode bypass)", rec.Code, http.StatusOK)
	}
}

func TestTokenEqual(t *testing.T) {
	if !TokenEqual("abc", "abc") {
		t.Error("TokenEqual(abc, abc) = false")
	}
	if TokenEqual("abc", "abd") {
		t.Error("TokenEqual(abc, abd) = true")
	}
	if TokenEqual("abc", "abcd") {
		t.Error("TokenEqual(abc, abcd) = true")
	}
	if TokenEqual("", "") {
		t.Error("TokenEqual empty strings should not match")
	}
}

func TestRejectWSAuthSends4401(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RejectWSAuth(w, r, log)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=bad"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, _, err = conn.ReadMessage()
	closeErr, ok := err.(*websocket.CloseError)
	if !ok {
		t.Fatalf("read error = %v, want *websocket.CloseError", err)
	}
	if closeErr.Code != WSAuthCloseCode {
		t.Fatalf("close code = %d, want %d", closeErr.Code, WSAuthCloseCode)
	}
	if closeErr.Text != "auth required" {
		t.Fatalf("close reason = %q, want %q", closeErr.Text, "auth required")
	}
}

func TestWSQueryToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws?token=abc123", nil)
	if got := WSQueryToken(req); got != "abc123" {
		t.Fatalf("WSQueryToken = %q, want %q", got, "abc123")
	}
	req = httptest.NewRequest(http.MethodGet, "/ws", nil)
	if got := WSQueryToken(req); got != "" {
		t.Fatalf("WSQueryToken missing = %q, want empty", got)
	}
}
