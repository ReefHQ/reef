package agents

import (
	"regexp"
	"time"
)

const MaxDiagnosticEvents = 20

var stableAgentVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func publicAgentVersion(v string) string {
	if stableAgentVersion.MatchString(v) {
		return v
	}
	if v == "dev" || v == "test" {
		return "dev"
	}
	return ""
}

type Observation struct {
	Code    string    `json:"code"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Hint    string    `json:"hint"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	Count   int       `json:"count"`
}
type Feature struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	State      string `json:"state"`
	Hint       string `json:"hint"`
}
type Diagnostic struct {
	Host                 View          `json:"host"`
	ObservedAt           time.Time     `json:"observed_at"`
	ConnectedAt          time.Time     `json:"connected_at"`
	LastInventoryAttempt time.Time     `json:"last_inventory_attempt"`
	ConnectionAge        *int64        `json:"contact_age_seconds"`
	InventoryAge         *int64        `json:"inventory_age_seconds"`
	Code                 string        `json:"code"`
	Level                string        `json:"level"`
	Summary              string        `json:"summary"`
	Hint                 string        `json:"hint"`
	Docker               string        `json:"docker"`
	ExpectedProtocol     int           `json:"expected_protocol"`
	NegotiatedProtocol   int           `json:"negotiated_protocol"`
	CapabilitiesCurrent  bool          `json:"capabilities_current"`
	Features             []Feature     `json:"features"`
	Observations         []Observation `json:"observations"`
}
type diagnosticState struct {
	Connected, Attempt, Contact, Checked time.Time
	Version                              string
	Capabilities                         []string
	Events                               []Observation
}

func age(now, at time.Time) *int64 {
	if at.IsZero() {
		return nil
	}
	v := int64(now.Sub(at) / time.Second)
	if v < 0 {
		v = 0
	}
	return &v
}

func observation(code string) Observation {
	o := Observation{Code: code, Level: "info"}
	switch code {
	case "connected":
		o.Message = "Authenticated agent connection accepted"
		o.Hint = "Waiting for this connection's first inventory."
	case "reconnected":
		o.Message = "Agent connection re-established"
		o.Hint = "New capabilities must be negotiated; interrupted operations may still need review."
	case "connection_replaced":
		o.Level = "warning"
		o.Message = "Previous agent connection replaced"
		o.Hint = "Expected during updates. If this repeats unexpectedly, check that the private identity is not shared between hosts."
	case "protocol_incompatible":
		o.Level = "error"
		o.Message = "Agent protocol is incompatible"
		o.Hint = "Use an agent compatible with this central Reef release. No command was sent by diagnostics."
	case "docker_unavailable":
		o.Level = "error"
		o.Message = "Local Docker inventory unavailable"
		o.Hint = "On the agent host, check Docker Engine, the configured socket mount and access permissions."
	case "inventory_limits":
		o.Level = "error"
		o.Message = "Docker inventory exceeds supported limits"
		o.Hint = "Check host inventory size and use a compatible agent; central keeps only its last valid inventory."
	case "inventory_rejected":
		o.Level = "error"
		o.Message = "Agent inventory payload rejected"
		o.Hint = "Check compatible agent versions and host inventory. The previous valid snapshot is retained."
	case "inventory_error":
		o.Level = "error"
		o.Message = "Agent inventory refresh failed; details withheld"
		o.Hint = "Inspect the agent's local logs and Docker access. Raw error text is withheld to protect credentials and paths."
	case "inventory_ready":
		o.Message = "First valid inventory received for this connection"
		o.Hint = "This reports inventory freshness, not the outcome of unrelated operations."
	case "inventory_recovered":
		o.Message = "Docker inventory refresh recovered"
		o.Hint = "A valid snapshot was received; previous failed operations are not automatically retried."
	case "request_failed":
		o.Level = "warning"
		o.Message = "Remote read could not be completed"
		o.Hint = "Check connectivity, agent status and request capacity; refresh the relevant resource before retrying."
	case "request_outcome_unknown":
		o.Level = "warning"
		o.Message = "Remote change response was interrupted; outcome may be unknown"
		o.Hint = "Inspect the host and operation report before retrying a change. A reconnection does not prove that it failed."
	case "remote_service_error":
		o.Level = "warning"
		o.Message = "A remote service replied with an error"
		o.Hint = "Check the relevant feature's status and agent setup. A service error alone does not prove that the agent disconnected."
	case "response_rejected":
		o.Level = "warning"
		o.Message = "Agent command response was rejected"
		o.Hint = "Check compatible versions and current agent connection. Do not replay a change without checking host state."
	case "revoked":
		o.Level = "warning"
		o.Message = "Agent access revoked"
		o.Hint = "If access is intended again, create a new enrollment and identity. Revoked credentials cannot reconnect."
	default:
		return Observation{}
	}
	return o
}

// Only fixed classifications enter the ring. No body, URL, path, argument,
// credential, generation, registry address or arbitrary agent error is retained.
func (r *Registry) observe(id, code string, now time.Time) {
	o := observation(code)
	if o.Code == "" {
		return
	}
	if r.diagnostics == nil {
		r.diagnostics = map[string]diagnosticState{}
	}
	d := r.diagnostics[id]
	o.First = now
	o.Last = now
	o.Count = 1
	if len(d.Events) > 0 && d.Events[len(d.Events)-1].Code == code {
		n := len(d.Events) - 1
		d.Events[n].Last = now
		if d.Events[n].Count < 1000000 {
			d.Events[n].Count++
		}
	} else {
		d.Events = append(d.Events, o)
		if len(d.Events) > MaxDiagnosticEvents {
			d.Events = append([]Observation{}, d.Events[len(d.Events)-MaxDiagnosticEvents:]...)
		}
	}
	r.diagnostics[id] = d
}
func inventoryCode(message string) string {
	switch message {
	case "":
		return ""
	case "Incompatible agent protocol", "Agent protocol is incompatible":
		return "protocol_incompatible"
	case "Local Docker inventory unavailable", "Docker unavailable":
		return "docker_unavailable"
	case "Local Docker inventory exceeds protocol limits", "Docker inventory exceeds supported limits":
		return "inventory_limits"
	case "Invalid monitoring sample", "Inventory exceeds limits", "Invalid container ID", "Invalid container state", "Invalid inventory field", "Central inventory cache limit reached", "Agent inventory payload rejected":
		return "inventory_rejected"
	default:
		return "inventory_error"
	}
}
func publicInventoryError(message string) string {
	if message == "" {
		return ""
	}
	return observation(inventoryCode(message)).Message
}

// ConnectionMarker stays inside central Reef and is never part of a browser DTO.
func (r *Registry) ConnectionMarker(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.data.Hosts[id]
	if !ok || h.Revoked {
		return ""
	}
	return r.live[id].Generation
}
func (r *Registry) Communication(id, marker, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.data.Hosts[id]
	if !ok || h.Revoked || marker == "" || r.live[id].Generation != marker {
		return
	}
	switch code {
	case "request_failed", "request_outcome_unknown", "remote_service_error", "response_rejected":
		r.observe(id, code, time.Now())
	}
}

func (r *Registry) Diagnostic(id string, now time.Time) (Diagnostic, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.data.Hosts[id]
	if !ok {
		return Diagnostic{}, false
	}
	v := r.live[id]
	d := r.diagnostics[id]
	view := r.viewAt(h, now)
	if h.Revoked {
		view.LastContact = d.Contact
		view.CheckedAt = d.Checked
		view.Version = d.Version
		view.Capabilities = append([]string{}, d.Capabilities...)
	}
	result := Diagnostic{Host: view, ObservedAt: now.UTC(), ConnectedAt: d.Connected, LastInventoryAttempt: d.Attempt, ConnectionAge: age(now, view.LastContact), InventoryAge: age(now, view.CheckedAt), ExpectedProtocol: Protocol, Docker: "unknown", Features: []Feature{}, Observations: []Observation{}}
	if !d.Connected.IsZero() {
		result.NegotiatedProtocol = Protocol
	}
	result.CapabilitiesCurrent = view.Status == "connected" && v.Generation != "" && v.Sequence > 0
	code := inventoryCode(v.Error)
	switch {
	case h.Revoked:
		code = "revoked"
	case code == "protocol_incompatible":
	case view.LastContact.IsZero():
		code = "never_seen"
	case view.Status != "connected":
		code = "disconnected"
	case code != "":
	case v.Sequence == 0:
		code = "awaiting_inventory"
	case view.Stale:
		code = "inventory_stale"
	case !v.Control:
		code = "inventory_only"
	default:
		code = "healthy"
	}
	result.Code = code
	o := observation(code)
	if o.Code != "" {
		result.Level = o.Level
		result.Summary = o.Message
		result.Hint = o.Hint
	} else {
		result.Level = "info"
		switch code {
		case "never_seen":
			result.Level = "warning"
			result.Summary = "No authenticated agent contact observed since central startup"
			result.Hint = "Check that the agent is running, its configured Reef address, trusted CA, routing/NAT and enrollment. Central cannot see pre-connection DNS, TLS or authentication failures."
		case "disconnected":
			result.Level = "warning"
			result.Summary = "Agent contact is older than 45 seconds"
			result.Hint = "Check the agent service and its outbound HTTPS connection. Inventory and capabilities shown are historical; a disconnect does not prove that a change failed."
		case "awaiting_inventory":
			result.Level = "warning"
			result.Summary = "Connected; waiting for the first inventory of this connection"
			result.Hint = "Allow the next heartbeat. If it does not arrive, check the agent's Docker access and local logs."
		case "inventory_stale":
			result.Level = "warning"
			result.Summary = "Agent contact exists, but Docker inventory is older than 15 seconds"
			result.Hint = "Refresh after the next heartbeat; if it remains stale, check agent load and Docker access."
		case "inventory_only":
			result.Level = "warning"
			result.Summary = "Agent inventory is current; control capabilities were not reported"
			result.Hint = "Upgrade the agent for logs, consoles and management. Capabilities are negotiated, not inferred from the version number."
		case "healthy":
			result.Summary = "Agent reachable; Docker inventory current"
			result.Hint = "This is based on existing connection/inventory signals. No diagnostic probe or repair command was sent."
		}
	}
	if view.Status == "connected" && !view.Stale && v.Sequence > 0 {
		result.Docker = "inventory_current"
	} else if view.Status == "connected" && inventoryCode(v.Error) == "docker_unavailable" {
		result.Docker = "reported_unavailable"
	} else if !view.CheckedAt.IsZero() {
		result.Docker = "last_snapshot_only"
	}
	features := []struct{ name, capability string }{{"Projects, logs and consoles", ControlCapability}, {"Images", ImagesCapability}, {"Large image batches", ImageBatchCapability}, {"Volumes", VolumesCapability}, {"Volume backups", VolumeBackupsCapability}, {"Networks", NetworksCapability}, {"Builds and cache", BuildsCapability}, {"Statistics", StatsCapability}, {"Container monitoring", MonitoringCapability}, {"Action events", EventsCapability}, {"Updates from Reef", UpdatesCapability}}
	for _, f := range features {
		reported := false
		for _, cap := range view.Capabilities {
			if cap == f.capability {
				reported = true
			}
		}
		item := Feature{Name: f.name, Capability: f.capability, State: "unknown", Hint: "No current capability report; reconnect the agent before drawing compatibility conclusions."}
		if result.CapabilitiesCurrent {
			if reported {
				item.State = "reported"
				item.Hint = "Negotiated for this connection; this is not an execution test."
			} else {
				item.State = "not_reported"
				item.Hint = "Upgrade a compatible agent and enable the feature when setup is required."
				if f.capability == UpdatesCapability {
					item.Hint = "Check the optional agent-updater setup; enable updates before requesting an upgrade from Reef."
				}
			}
		} else if reported {
			item.State = "historical"
			item.Hint = "Reported by the previous connection; unavailable until a current report is received."
		}
		result.Features = append(result.Features, item)
	}
	for i := len(d.Events) - 1; i >= 0; i-- {
		result.Observations = append(result.Observations, d.Events[i])
	}
	return result, true
}
