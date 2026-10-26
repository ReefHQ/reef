package app

import (
	"net/http"
	"reef/internal/buildinfo"
)

// Health does not expose inventory or touch Docker. It becomes reachable only
// after startup has restored private state and acquired the instance lock.
func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Method == "HEAD" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		return
	}
	writeJSON(w, struct {
		Status   string `json:"status"`
		Version  string `json:"version"`
		Revision string `json:"revision"`
	}{"ok", buildinfo.Version, buildinfo.Revision})
}
