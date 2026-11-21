package api

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
}

// HealthzHandler reports process liveness without external dependency checks
func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}
