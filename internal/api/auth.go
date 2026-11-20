package api

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// CORS wraps next with permissive local-dev CORS headers. It must run
// BEFORE auth so OPTIONS preflight requests succeed without credentials.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TokenEqual compares two tokens in constant time. Empty inputs never
// match; length mismatch short-circuits; contents use crypto/subtle.
func TokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// BearerAuth protects routes with `Authorization: Bearer <token>`.
// An empty token disables auth entirely (dev-mode bypass) so local
// development and tests keep working without ADMIN_API_TOKEN.
func BearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) || !TokenEqual(auth[len(prefix):], token) {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}

// WSAuthCloseCode is the WebSocket close code sent when the ?token=
// query param is missing or invalid. The UI treats 4401/4001 or an
// "auth" reason as AUTH REQUIRED.
const WSAuthCloseCode = 4401

// RejectWSAuth upgrades the connection and immediately closes it with
// WSAuthCloseCode so the client surfaces an auth-required state instead
// of retrying the plain HTTP path.
func RejectWSAuth(w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn("ws auth-reject upgrade failed", "err", err)
		return
	}
	deadline := time.Now().Add(writeWait)
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(WSAuthCloseCode, "auth required"),
		deadline,
	)
	_ = conn.Close()
}

// WSQueryToken extracts the ?token= credential used by the UI's
// wsUrl() helper.
func WSQueryToken(r *http.Request) string {
	return r.URL.Query().Get("token")
}

// WriteError renders a JSON {"error": msg} response with the given status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// WriteJSON renders v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON decodes a single JSON object body into v.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return err
	}
	return nil
}
