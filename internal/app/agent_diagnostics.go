package app

import (
	"context"
	"io"
	"net/http"
	"reef/internal/agents"
	"reef/internal/buildinfo"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (s *Server) agentDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if s.agents == nil {
		http.Error(w, "Agent registry unavailable", 503)
		return
	}
	host := r.URL.Query().Get("host")
	if !s.requestUser(r).Allows(host) {
		http.Error(w, "Host access denied", 403)
		return
	}
	d, ok := s.agents.Diagnostic(host, time.Now())
	if !ok {
		http.Error(w, "Agent not found", 404)
		return
	}
	relation := "unreported"
	if d.Host.Version != "" {
		relation = "not_comparable"
	}
	if d.Host.Version == "dev" {
		relation = "development"
	} else if cmp, ok := compareAgentVersions(d.Host.Version, buildinfo.Version); ok {
		relation = "same"
		if cmp < 0 {
			relation = "older"
		}
		if cmp > 0 {
			relation = "newer"
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"diagnostic": d, "agents_enabled": s.cfg.AgentsEnabled, "central_version": buildinfo.Version, "version_relation": relation, "history_limit": agents.MaxDiagnosticEvents, "scope": "since_central_startup"})
}
func compareAgentVersions(a, b string) (int, bool) {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	if len(x) != 3 || len(y) != 3 {
		return 0, false
	}
	result := 0
	for i := 0; i < 3; i++ {
		av, ae := strconv.ParseUint(x[i], 10, 32)
		bv, be := strconv.ParseUint(y[i], 10, 32)
		if ae != nil || be != nil {
			return 0, false
		}
		if result == 0 {
			if av < bv {
				result = -1
			}
			if av > bv {
				result = 1
			}
		}
	}
	return result, true
}
func (p *remoteDocker) diagnosticFailure(ctx context.Context, marker, method, code string) {
	if p.server.agents == nil || ctx.Err() == context.Canceled {
		return
	}
	if code == "" {
		code = "request_failed"
		if method == "POST" {
			code = "request_outcome_unknown"
		}
	}
	p.server.agents.Communication(p.host, marker, code)
}

type diagnosticBody struct {
	io.ReadCloser
	once   sync.Once
	report func()
}

func (b *diagnosticBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil && e != io.EOF {
		b.once.Do(b.report)
	}
	return n, e
}
