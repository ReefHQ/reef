package app

import (
	"net/http"
	"reef/internal/accounts"
	"strconv"
)

func (s *Server) userAccounts(w http.ResponseWriter, r *http.Request, sess session) {
	if s.accounts == nil {
		http.Error(w, "Accounts are configured on central Reef only", 409)
		return
	}
	me := s.principal(sess)
	if r.Method == "GET" {
		if r.URL.Path == "/api/me" {
			writeJSON(w, map[string]any{"user": me, "instance_admin": me.GlobalAdmin()})
			return
		}
		writeJSON(w, map[string]any{"users": s.accounts.List()})
		return
	}
	if r.Method != "POST" || r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Confirm account changes", 400)
		return
	}
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		http.Error(w, "Account update busy; try again shortly", 429)
		return
	}
	if r.URL.Path == "/api/me" {
		defer func() {
			current, ok := s.accounts.User(me.ID)
			if !ok || current.Revision != me.Revision {
				s.revokeUserSessions(me.ID)
			}
		}()
		switch r.Form.Get("action") {
		case "password":
			if r.Form.Get("password") != r.Form.Get("password_confirmation") {
				http.Error(w, "Passwords do not match", 400)
				return
			}
			if e := s.accounts.ChangePassword(me.ID, r.Form.Get("current_password"), r.Form.Get("password")); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
		case "revoke":
			if _, e := s.accounts.ChangeBy(me, me, "", false, false); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
		default:
			http.Error(w, "Unknown account action", 400)
			return
		}
		writeJSON(w, map[string]bool{"sign_in_required": true})
		return
	}
	action := r.Form.Get("action")
	u := accounts.User{Username: r.Form.Get("username"), Role: r.Form.Get("role"), AllHosts: r.Form.Get("all_hosts") == "true", Hosts: r.Form["hosts"], Disabled: r.Form.Get("disabled") == "true"}
	if action != "create" {
		var ok bool
		u, ok = s.accounts.User(r.Form.Get("id"))
		if !ok {
			http.Error(w, "Account not found", 404)
			return
		}
		expected, e := strconv.ParseUint(r.Form.Get("revision"), 10, 64)
		if e != nil || u.Revision != expected {
			http.Error(w, "Account changed; refresh before editing", 409)
			return
		}
		if action == "update" {
			u.Role = r.Form.Get("role")
			u.AllHosts = r.Form.Get("all_hosts") == "true"
			u.Hosts = r.Form["hosts"]
			u.Disabled = r.Form.Get("disabled") == "true"
		}
	}
	if action != "create" && action != "update" && action != "delete" && action != "revoke" {
		http.Error(w, "Unknown account action", 400)
		return
	}
	if (action == "create" || action == "update") && !u.Disabled {
		for _, host := range u.Hosts {
			if host != "local" {
				if s.agents == nil {
					http.Error(w, "Unknown host assignment", 400)
					return
				}
				h, _, ok := s.agents.Inventory(host)
				if !ok || h.Status == "revoked" {
					http.Error(w, "Unknown or revoked host assignment", 400)
					return
				}
			}
		}
	}
	password := r.Form.Get("password")
	if action == "create" || password != "" {
		if password != r.Form.Get("password_confirmation") {
			http.Error(w, "Passwords do not match", 400)
			return
		}
	}
	before := u.Revision
	changed, e := s.accounts.ChangeBy(me, u, password, action == "delete", true)
	current, exists := s.accounts.User(u.ID)
	if u.ID != "" && (!exists || current.Revision != before) {
		s.revokeUserSessions(u.ID)
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = changed
	writeJSON(w, map[string]bool{"updated": true, "sign_in_required": u.ID == me.ID})
}
