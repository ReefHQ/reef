package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reef/internal/notifications"
	"reef/internal/schedules"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This context identity is created only inside central Reef, never from headers,
// cookies or an agent. No durable browser session or password is minted.
type scheduleSessionKey struct{}
type scheduleIdentity struct {
	Session session
	Job     schedules.Job
}
type scheduleResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *scheduleResponse) Header() http.Header { return w.header }
func (w *scheduleResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *scheduleResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(p) > 16<<20 {
		return 0, errors.New("Schedule response too large")
	}
	return w.body.Write(p)
}

type scheduleCallError struct{ status int }

func (e scheduleCallError) Error() string {
	switch e.status {
	case 401, 403:
		return "Schedule authorization changed; sign in again or reauthorize the schedule"
	case 404:
		return "Target no longer exists on this host"
	case 409:
		return "Host is busy, needs an agent upgrade, or the target configuration changed"
	case 503:
		return "Host is disconnected or its service/storage is unavailable"
	}
	return "Host API unavailable or operation rejected"
}
func (s *Server) scheduleIdentityValid(r *http.Request, v scheduleIdentity) bool {
	if s.accounts == nil || r.Context().Err() != nil || !time.Now().Before(v.Session.Expires) {
		return false
	}
	u, ok := s.accounts.User(v.Session.UserID)
	host := r.URL.Query().Get("host")
	if host == "" {
		host = "local"
	}
	if !ok || u.Disabled || u.Revision != v.Session.Revision || !u.Allows(host) {
		return false
	}
	if r.Method == "POST" {
		if u.Role != "admin" || s.schedules == nil || v.Job.ID == "" {
			return false
		}
		rows, e := s.schedules.List()
		if e != nil {
			return false
		}
		for _, j := range rows {
			if j.ID == v.Job.ID {
				expected := map[string]string{"image_check": "/api/image-updates", "volume_backup": "/api/volume-backups", "image_cleanup": "/api/images/action", "cache_cleanup": "/api/builds/cache/action"}
				if j.Action == "image_check" && r.PostForm.Get("id") != j.Target {
					return false
				}
				if j.Action == "volume_backup" && (r.PostForm.Get("action") != "backup" || r.PostForm.Get("volume") != j.Target || r.PostForm.Get("fingerprint") != j.Fingerprint) {
					return false
				}
				if j.Action == "image_cleanup" && r.PostForm.Get("action") != "cleanup" {
					return false
				}
				return r.URL.Path == expected[j.Action] && j.Host == host && j.Enabled && j.Revision == v.Job.Revision && j.Last.ID == v.Job.Last.ID && j.Last.Status == "running"
			}
		}
		return false
	}
	return r.Method == "GET"
}
func (s *Server) scheduleInvoke(ctx context.Context, identity scheduleIdentity, host, method, path string, form url.Values, out any) error {
	if ctx.Err() != nil {
		if method == "POST" {
			return scheduleCallError{409}
		}
		return ctx.Err()
	}
	scheme := "http"
	if s.cfg.SecureCookie {
		scheme = "https"
	}
	u, e := url.Parse(scheme + "://reef-scheduler.internal" + path)
	if e != nil {
		return e
	}
	q := u.Query()
	q.Set("host", host)
	u.RawQuery = q.Encode()
	fields := url.Values{}
	for k, values := range form {
		fields[k] = append([]string{}, values...)
	}
	fields.Set("csrf", identity.Session.CSRF)
	r, e := http.NewRequestWithContext(context.WithValue(ctx, scheduleSessionKey{}, identity), method, u.String(), strings.NewReader(fields.Encode()))
	if e != nil {
		return e
	}
	r.Header.Set("Origin", scheme+"://reef-scheduler.internal")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.PostForm = fields
	w := &scheduleResponse{header: http.Header{}}
	s.Handler().ServeHTTP(w, r)
	if w.status < 200 || w.status >= 300 {
		return scheduleCallError{w.status}
	}
	if out != nil {
		if e = json.Unmarshal(w.body.Bytes(), out); e != nil {
			return errors.New("Invalid schedule response; inspect host outcome")
		}
	}
	return nil
}

type scheduleProject struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Directory   string       `json:"directory"`
	Files       []string     `json:"files"`
	ProjectName string       `json:"project_name"`
	Busy        bool         `json:"busy"`
	Updates     ImageUpdates `json:"image_updates"`
}

func (p scheduleProject) fingerprint() string {
	b, _ := json.Marshal([]any{p.ID, p.Name, p.Directory, p.Files, p.ProjectName})
	return digest(b)
}

type scheduleTarget struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Fingerprint string       `json:"fingerprint"`
	Consumers   []backupUser `json:"consumers,omitempty"`
}

func (s *Server) scheduleTargets(ctx context.Context, identity scheduleIdentity, host, kind string) ([]scheduleTarget, error) {
	if host != "local" {
		if !s.remoteReady(host) {
			return nil, errors.New("Connect this host before reviewing a schedule")
		}
		if kind == "volume_backup" && !s.agents.CanVolumeBackups(host) {
			return nil, errors.New("Upgrade this agent to v0.18.0 or newer for scheduled volume backups")
		}
	}
	result := []scheduleTarget{}
	switch kind {
	case "image_check":
		var rows []scheduleProject
		if e := s.scheduleInvoke(ctx, identity, host, "GET", "/api/state", nil, &rows); e != nil {
			return nil, e
		}
		for _, p := range rows {
			result = append(result, scheduleTarget{ID: p.ID, Name: p.Name, Fingerprint: p.fingerprint()})
		}
	case "volume_backup":
		var data struct{ Volumes []CatalogVolume }
		if e := s.scheduleInvoke(ctx, identity, host, "GET", "/api/volumes", nil, &data); e != nil {
			return nil, e
		}
		for _, v := range data.Volumes {
			if v.Driver == "local" && v.Scope == "local" {
				result = append(result, scheduleTarget{ID: v.Name, Name: v.Name, Fingerprint: v.Fingerprint})
			}
		}
	case "image_cleanup", "cache_cleanup": // Capabilities/storage access are verified without selecting a deletion target.
		path := "/api/images"
		if kind == "cache_cleanup" {
			path = "/api/builds/cache"
		}
		var data json.RawMessage
		if e := s.scheduleInvoke(ctx, identity, host, "GET", path, nil, &data); e != nil {
			return nil, e
		}
	default:
		return nil, errors.New("Choose a supported schedule action")
	}
	return result, nil
}
func (s *Server) scheduleOwnerValid(j schedules.Job) bool {
	if s.accounts == nil {
		return false
	}
	u, ok := s.accounts.User(j.Owner)
	return ok && !u.Disabled && u.Role == "admin" && u.Revision == j.OwnerRevision && u.Allows(j.Host)
}
func (s *Server) scheduleAPI(w http.ResponseWriter, r *http.Request, sess session) {
	if s.schedules == nil {
		http.Error(w, "Central schedule storage unavailable; inspect private schedule state", 503)
		return
	}
	host := r.URL.Query().Get("host")
	if host == "" {
		host = "local"
	}
	u := s.requestUser(r)
	if r.Method == "GET" {
		if r.URL.Query().Get("preview") == "true" {
			s.schedulePreview(w, r, sess, host)
			return
		}
		if r.URL.Query().Get("targets") == "true" {
			if !u.Allows(host) {
				http.Error(w, "Host access denied", 403)
				return
			}
			items, e := s.scheduleTargets(r.Context(), scheduleIdentity{Session: sess}, host, r.URL.Query().Get("kind"))
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			writeJSON(w, map[string]any{"targets": items, "draft": randomID()})
			return
		}
		rows, e := s.schedules.List()
		if e != nil {
			http.Error(w, e.Error(), 503)
			return
		}
		items := []map[string]any{}
		for _, j := range rows {
			if !u.Allows(j.Host) || (r.URL.Query().Get("all") != "true" && j.Host != host) {
				continue
			}
			b, _ := json.Marshal(j)
			v := map[string]any{}
			json.Unmarshal(b, &v)
			v["host_name"] = s.scheduleHostName(j.Host)
			v["authorization_valid"] = s.scheduleOwnerValid(j)
			items = append(items, v)
		}
		writeJSON(w, map[string]any{"schedules": items})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Form.Get("confirmed") != "yes" {
		http.Error(w, "Explicit schedule/policy confirmation required", 400)
		return
	}
	revision, e := strconv.ParseUint(r.Form.Get("revision"), 10, 64)
	if e != nil {
		http.Error(w, "Invalid schedule revision", 400)
		return
	}
	rows, e := s.schedules.List()
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	id := r.Form.Get("id")
	var old *schedules.Job
	for i := range rows {
		if rows[i].ID == id {
			old = &rows[i]
			break
		}
	}
	if id != "" && ((old == nil && revision != 0) || (old != nil && (old.Host != host || !u.Allows(old.Host)))) {
		http.Error(w, "Schedule not found on this host", 404)
		return
	}
	action := r.Form.Get("action")
	eventName := ""
	if old != nil {
		eventName = old.Name
	}
	switch action {
	case "disable":
		e = s.schedules.Disable(id, revision)
	case "delete":
		e = s.schedules.Remove(id, revision, r.Form.Get("reviewed") == "yes")
	case "save":
		enabled := r.Form.Get("enabled")
		if enabled != "true" && enabled != "false" {
			http.Error(w, "Choose enabled or disabled", 400)
			return
		}
		weekday, we := strconv.Atoi(r.Form.Get("weekday"))
		if we != nil {
			http.Error(w, "Choose a valid weekday", 400)
			return
		}
		age, _ := strconv.Atoi(r.Form.Get("age_days"))
		maxItems, _ := strconv.Atoi(r.Form.Get("max_items"))
		j := schedules.Job{ID: id, Name: strings.TrimSpace(r.Form.Get("name")), Host: host, Action: r.Form.Get("kind"), Target: r.Form.Get("target"), Fingerprint: r.Form.Get("fingerprint"), Frequency: r.Form.Get("frequency"), Weekday: weekday, Time: r.Form.Get("time"), Zone: r.Form.Get("zone"), AgeDays: age, MaxItems: maxItems, StopConsumers: r.Form.Get("stop_consumers") == "true", Enabled: enabled == "true", Owner: u.ID, OwnerRevision: u.Revision}
		identity := scheduleIdentity{Session: sess}
		targets, err := s.scheduleTargets(r.Context(), identity, host, j.Action)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		if j.Action == "image_check" || j.Action == "volume_backup" {
			found := false
			for _, t := range targets {
				if t.ID == j.Target && t.Fingerprint == j.Fingerprint {
					found = true
				}
			}
			if !found {
				http.Error(w, "Target changed; refresh and review the policy", 409)
				return
			}
		}
		if j.Action == "volume_backup" {
			var preview struct{ Preview backupPreview }
			path := "/api/volume-backups?" + url.Values{"volume": {j.Target}, "fingerprint": {j.Fingerprint}}.Encode()
			if err = s.scheduleInvoke(r.Context(), identity, host, "GET", path, nil, &preview); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
		}
		eventName = j.Name
		_, e = s.schedules.Change(j, revision, r.Form.Get("reviewed") == "yes", time.Now())
	default:
		http.Error(w, "Unsupported schedule action", 400)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	kind := "configure"
	if action == "delete" {
		kind = "remove"
	}
	s.scheduleEvent(schedules.Job{Host: host, Name: eventName}, kind, "completed", randomID())
	writeJSON(w, map[string]bool{"ok": true})
}
func (s *Server) schedulePreview(w http.ResponseWriter, r *http.Request, sess session, host string) {
	identity := scheduleIdentity{Session: sess}
	kind := r.URL.Query().Get("kind")
	result := map[string]any{"count": 0, "targets": []string{}, "consumers": []backupUser{}}
	switch kind {
	case "volume_backup":
		var data struct{ Preview backupPreview }
		path := "/api/volume-backups?" + url.Values{"volume": {r.URL.Query().Get("target")}, "fingerprint": {r.URL.Query().Get("fingerprint")}}.Encode()
		if e := s.scheduleInvoke(r.Context(), identity, host, "GET", path, nil, &data); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		result["consumers"] = data.Preview.Containers
		result["count"] = 1
	case "image_cleanup", "cache_cleanup":
		age, ae := strconv.Atoi(r.URL.Query().Get("age_days"))
		max, me := strconv.Atoi(r.URL.Query().Get("max_items"))
		if ae != nil || me != nil || age < 1 || age > 365 || max < 1 || max > 2000 {
			http.Error(w, "Choose 1–365 days and 1–2000 targets", 400)
			return
		}
		j := schedules.Job{AgeDays: age, MaxItems: max}
		ids := []string{}
		if kind == "image_cleanup" {
			var data struct{ Images []CatalogImage }
			if e := s.scheduleInvoke(r.Context(), identity, host, "GET", "/api/images", nil, &data); e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			for _, v := range scheduledImages(data.Images, j, time.Now()) {
				ids = append(ids, v.ID)
			}
		} else {
			var data struct{ Records []BuildCacheRecord }
			if e := s.scheduleInvoke(r.Context(), identity, host, "GET", "/api/builds/cache", nil, &data); e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
			for _, v := range scheduledCache(data.Records, j, time.Now()) {
				ids = append(ids, v.ID)
			}
		}
		result["count"] = len(ids)
		result["targets"] = ids[:min(10, len(ids))]
	case "image_check":
		items, e := s.scheduleTargets(r.Context(), identity, host, kind)
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		found := false
		for _, v := range items {
			if v.ID == r.URL.Query().Get("target") && v.Fingerprint == r.URL.Query().Get("fingerprint") {
				found = true
				result["targets"] = []string{v.Name}
				result["count"] = 1
			}
		}
		if !found {
			http.Error(w, "Project changed; refresh its target", 409)
			return
		}
	default:
		http.Error(w, "Unsupported schedule action", 400)
		return
	}
	writeJSON(w, result)
}
func (s *Server) scheduleHostName(host string) string {
	if host == "local" {
		return "Local"
	}
	if s.agents != nil {
		for _, h := range s.agents.Views() {
			if h.ID == host {
				return h.Name
			}
		}
	}
	return "Unavailable host " + host[:min(12, len(host))]
}
func (s *Server) scheduleEvent(j schedules.Job, action, status, run string) {
	e := notifications.New("schedule", action, status, run, j.Name)
	e.HostID = j.Host
	e.Host = s.scheduleHostName(j.Host)
	e.Count = 1
	if status == "completed" {
		e.Succeeded = 1
	}
	if status == "failed" {
		e.Failed = 1
	}
	if status == "partial" {
		e.Skipped = 1
	}
	if s.history != nil {
		_ = s.history.AcceptCentral(e)
	}
	if s.webhooks != nil {
		_ = s.webhooks.AcceptCentral(e)
	}
}

func (s *Server) runSchedules(ctx context.Context) {
	if s.schedules == nil {
		return
	}
	s.m.wg.Add(1)
	go func() {
		defer s.m.wg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.scheduleTick(ctx, now)
			}
		}
	}()
}
func (s *Server) scheduleTick(ctx context.Context, now time.Time) {
	rows, e := s.schedules.Due(now)
	if e != nil {
		return
	}
	for _, candidate := range rows {
		if ctx.Err() != nil {
			return
		}
		j, e := s.schedules.Claim(candidate.ID, candidate.Revision, now)
		if e != nil {
			continue
		}
		s.scheduleMu.Lock()
		reason := ""
		if s.scheduleActive[j.Host] {
			reason = "Another scheduled operation is active on this host"
		} else if len(s.scheduleActive) >= 4 {
			reason = "Scheduler capacity is busy"
		}
		all, e := s.schedules.List()
		if e != nil {
			reason = "Schedule storage unavailable"
		}
		for _, other := range all {
			if other.Host == j.Host && other.ReviewRequired {
				reason = "An uncertain scheduled outcome on this host needs review"
			}
		}
		if now.Sub(j.Last.Due) > time.Minute {
			reason = "Scheduled slot missed; no catch-up"
		}
		if !s.scheduleOwnerValid(j) {
			reason = "Schedule authorization changed; edit and explicitly reauthorize"
			_ = s.schedules.Disable(j.ID, j.Revision)
		}
		if j.Host != "local" && !s.remoteReady(j.Host) {
			reason = "Host disconnected or unavailable at the scheduled time"
		}
		if reason != "" {
			s.scheduleMu.Unlock()
			s.finishSchedule(j, "skipped", reason, "")
			continue
		}
		s.scheduleActive[j.Host] = true
		s.m.wg.Add(1)
		s.scheduleMu.Unlock()
		s.scheduleEvent(j, "run", "started", j.Last.ID)
		go func(j schedules.Job) {
			defer s.m.wg.Done()
			defer func() { s.scheduleMu.Lock(); delete(s.scheduleActive, j.Host); s.scheduleMu.Unlock() }()
			runCtx, cancel := context.WithTimeout(ctx, 45*time.Minute+30*time.Second)
			defer cancel()
			status, message, op := s.executeSchedule(runCtx, j)
			s.finishSchedule(j, status, message, op)
		}(j)
	}
}
func (s *Server) finishSchedule(j schedules.Job, status, message, op string) {
	if e := s.schedules.Finish(j.ID, j.Last.ID, status, message, op, time.Now()); e != nil {
		return
	}
	eventStatus := status
	if status == "skipped" {
		eventStatus = "partial"
	}
	s.scheduleEvent(j, "run", eventStatus, j.Last.ID)
}
func scheduleFailure(err error, mutation bool) (string, string) {
	var call scheduleCallError
	if errors.As(err, &call) && call.status >= 400 && call.status < 500 {
		return "skipped", "Operation rejected before admission; host busy, target changed or authorization revoked"
	}
	if mutation {
		return "unknown", "Host outcome uncertain. Inspect the host and acknowledge before re-enabling; no automatic replay."
	}
	return "failed", "Host data unavailable or authorization changed; no mutation was dispatched"
}
func (s *Server) executeSchedule(ctx context.Context, j schedules.Job) (string, string, string) {
	identity := scheduleIdentity{Session: session{UserID: j.Owner, Revision: j.OwnerRevision, CSRF: randomID(), Expires: time.Now().Add(time.Hour)}, Job: j}
	get := func(path string, out any) error {
		return s.scheduleInvoke(ctx, identity, j.Host, "GET", path, nil, out)
	}
	post := func(path string, form url.Values, out any) error {
		return s.scheduleInvoke(ctx, identity, j.Host, "POST", path, form, out)
	}
	fail := func(e error, mutation bool) (string, string, string) {
		status, message := scheduleFailure(e, mutation)
		return status, message, ""
	}
	var op CatalogOperation
	operationPath := ""
	form := url.Values{"confirmed": {"yes"}}
	switch j.Action {
	case "image_check":
		var rows []scheduleProject
		if e := get("/api/state", &rows); e != nil {
			return fail(e, false)
		}
		var p *scheduleProject
		for i := range rows {
			if rows[i].ID == j.Target {
				p = &rows[i]
			}
		}
		if p == nil || p.fingerprint() != j.Fingerprint {
			return "skipped", "Project identity changed; review the schedule", ""
		}
		if p.Busy || p.Updates.Checking {
			return "skipped", "Project operation or image check already running", ""
		}
		before := time.Now()
		if e := post("/api/image-updates", url.Values{"id": {j.Target}}, nil); e != nil {
			return fail(e, false)
		}
		for {
			if e := get("/api/state", &rows); e != nil {
				return fail(e, false)
			}
			found := false
			for _, row := range rows {
				if row.ID != j.Target {
					continue
				}
				found = true
				if !row.Updates.Checking {
					if row.Updates.CheckedAt.Before(before) {
						return "skipped", "A recent image check already satisfied the cooldown", ""
					}
					if row.Updates.Error != "" {
						return "failed", "Image update check failed; inspect the project report", ""
					}
					return "completed", "Image update check completed; no deployment was performed", ""
				}
			}
			if !found {
				return "failed", "Project removed while checking", ""
			}
			if e := scheduleWait(ctx); e != nil {
				return fail(e, false)
			}
		}
	case "volume_backup":
		var data struct {
			Preview   backupPreview
			Operation CatalogOperation
		}
		path := "/api/volume-backups?" + url.Values{"volume": {j.Target}, "fingerprint": {j.Fingerprint}}.Encode()
		if e := get(path, &data); e != nil {
			return fail(e, false)
		}
		if data.Operation.Running {
			return "skipped", "A backup operation is already running", ""
		}
		form.Set("action", "backup")
		form.Set("volume", j.Target)
		form.Set("fingerprint", j.Fingerprint)
		form.Set("usage", data.Preview.Usage)
		if e := post("/api/volume-backups", form, &op); e != nil {
			return fail(e, true)
		}
		operationPath = "/api/volume-backups"
	case "image_cleanup":
		var data struct {
			Images    []CatalogImage
			Operation CatalogOperation
		}
		if e := get("/api/images", &data); e != nil {
			return fail(e, false)
		}
		if data.Operation.Running {
			return "skipped", "Image operation already running", ""
		}
		targets := scheduledImages(data.Images, j, time.Now())
		if len(targets) == 0 {
			return "completed", "No eligible old, untagged, unused images", ""
		}
		raw, _ := json.Marshal(targets)
		form.Set("action", "cleanup")
		form.Set("targets", string(raw))
		if e := post("/api/images/action", form, &op); e != nil {
			return fail(e, true)
		}
		operationPath = "/api/images/operation"
	case "cache_cleanup":
		var data struct {
			Records   []BuildCacheRecord
			Operation CatalogOperation
		}
		if e := get("/api/builds/cache", &data); e != nil {
			return fail(e, false)
		}
		if data.Operation.Running {
			return "skipped", "Build cache operation already running", ""
		}
		targets := scheduledCache(data.Records, j, time.Now())
		if len(targets) == 0 {
			return "completed", "No eligible old, unused, unshared build cache", ""
		}
		raw, _ := json.Marshal(targets)
		form.Set("targets", string(raw))
		if e := post("/api/builds/cache/action", form, &op); e != nil {
			return fail(e, true)
		}
		operationPath = "/api/builds/cache/operation"
	default:
		return "failed", "Unsupported schedule action", ""
	}
	if !validScheduleOperation(op) {
		return "unknown", "Operation was admitted but its identity is unavailable; inspect the host", ""
	}
	operationID := op.ID
	for {
		if !op.Running {
			if op.Total > 0 && len(op.Results) == 0 {
				return "unknown", "Operation finished without a verifiable result; inspect the host", operationID
			}
			if op.Skipped > 0 || len(op.Results) != op.Total {
				return "failed", "Operation finished with skipped or missing results; inspect its resource report", operationID
			}
			for _, r := range op.Results {
				if !r.OK || r.Unprocessed {
					return "failed", "Operation finished with retained targets or errors; inspect its resource report", operationID
				}
			}
			return "completed", "Operation completed under the approved policy", operationID
		}
		if e := scheduleWait(ctx); e != nil {
			status, message := scheduleFailure(e, true)
			return status, message, operationID
		}
		var e error
		if operationPath == "/api/volume-backups" {
			var data struct{ Operation CatalogOperation }
			e = get(operationPath, &data)
			op = data.Operation
		} else {
			e = get(operationPath, &op)
		}
		if e != nil {
			status, message := scheduleFailure(e, true)
			if status == "skipped" {
				status = "unknown"
				message = "Admitted operation can no longer be observed; inspect host outcome before acknowledging"
			}
			return status, message, operationID
		}
		if op.ID != operationID {
			return "unknown", "Operation report replaced before its result was observed; inspect the host", operationID
		}
	}
}
func validScheduleOperation(op CatalogOperation) bool {
	b, e := hex.DecodeString(op.ID)
	return e == nil && len(b) == 24 && !op.Started.IsZero() && op.Total >= 1 && op.Total <= 2000
}
func scheduleWait(ctx context.Context) error {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func scheduledImages(rows []CatalogImage, j schedules.Job, now time.Time) []ImageTarget {
	sort.Slice(rows, func(i, k int) bool { return rows[i].Created < rows[k].Created })
	out := []ImageTarget{}
	for _, r := range rows {
		if len(r.Tags) == 0 && len(r.Digests) == 0 && len(r.Containers) == 0 && scheduleOld(r.Created, "", j.AgeDays, now) {
			out = append(out, ImageTarget{r.ID, r.Fingerprint})
			if len(out) >= j.MaxItems {
				break
			}
		}
	}
	return out
}
func scheduledCache(rows []BuildCacheRecord, j schedules.Job, now time.Time) []CacheTarget {
	sort.Slice(rows, func(i, k int) bool { return rows[i].Created < rows[k].Created })
	out := []CacheTarget{}
	for _, r := range rows {
		if r.Reclaimable && !r.Mutable && !r.Shared && scheduleOld(r.Created, r.LastUsed, j.AgeDays, now) {
			out = append(out, CacheTarget{r.ID, r.Fingerprint})
			if len(out) >= j.MaxItems {
				break
			}
		}
	}
	return out
}
func scheduleOld(created, last string, days int, now time.Time) bool {
	if days < 1 || days > 365 {
		return false
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	c, e := time.Parse(time.RFC3339Nano, created)
	if e != nil || c.IsZero() || c.After(cutoff) {
		return false
	}
	if last != "" {
		t, e := time.Parse(time.RFC3339Nano, last)
		if e != nil || (!t.IsZero() && t.After(cutoff)) {
			return false
		}
	}
	return true
}

var _ io.Writer = (*scheduleResponse)(nil)
