package main

// Per-device service reports shown on the relay page. The phone<->PC link is end-to-end
// encrypted, so the relay can't see a session. Instead the phone, which has rendered the report,
// posts it here (over its paired session) keyed by the device. The relay stores and displays it,
// and lets the technician remove a device and its reports.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type reportMeta struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	OS      string    `json:"os"`
	Summary string    `json:"summary"`
	When    time.Time `json:"when"`
}

const maxReports = 300
const maxReportHTML = 1 << 20

func (r *Relay) reportsDir() string { return filepath.Join(r.dir, "reports") }

func (r *Relay) loadReports() {
	r.reports = map[string]*reportMeta{}
	ents, _ := os.ReadDir(r.reportsDir())
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			if b, err := os.ReadFile(filepath.Join(r.reportsDir(), e.Name())); err == nil {
				var m reportMeta
				if json.Unmarshal(b, &m) == nil && m.ID != "" {
					r.reports[m.ID] = &m
				}
			}
		}
	}
}

func safeID(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteRune(c)
		}
	}
	id := b.String()
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// phoneReport stores a report the phone posts for a device (authed as a paired phone).
func (r *Relay) phoneReport(w http.ResponseWriter, req *http.Request) {
	var in struct{ ID, Name, OS, Summary, HTML string }
	if readJSON(req, &in, maxReportHTML+8192) != nil {
		fail(w, 400, "bad request")
		return
	}
	id := safeID(in.ID)
	if id == "" || in.HTML == "" {
		fail(w, 400, "missing device id or report")
		return
	}
	if len(in.HTML) > maxReportHTML {
		in.HTML = in.HTML[:maxReportHTML]
	}
	os.MkdirAll(r.reportsDir(), 0700)
	m := &reportMeta{ID: id, Name: clip(in.Name, 60), OS: clip(in.OS, 20), Summary: clip(in.Summary, 200), When: time.Now()}
	if err := os.WriteFile(filepath.Join(r.reportsDir(), id+".html"), []byte(in.HTML), 0600); err != nil {
		fail(w, 500, "couldn't save report")
		return
	}
	mb, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(r.reportsDir(), id+".json"), mb, 0600)
	r.mu.Lock()
	if r.reports == nil {
		r.reports = map[string]*reportMeta{}
	}
	r.reports[id] = m
	r.pruneReportsLocked()
	r.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) pruneReportsLocked() {
	if len(r.reports) <= maxReports {
		return
	}
	var all []*reportMeta
	for _, m := range r.reports {
		all = append(all, m)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].When.Before(all[j].When) })
	for _, m := range all[:len(all)-maxReports] {
		r.deleteReportFiles(m.ID)
		delete(r.reports, m.ID)
	}
}

func (r *Relay) deleteReportFiles(id string) {
	os.Remove(filepath.Join(r.reportsDir(), id+".html"))
	os.Remove(filepath.Join(r.reportsDir(), id+".json"))
}

// uiReport serves a stored report's HTML to a logged-in technician (sandboxed: no scripts).
func (r *Relay) uiReport(w http.ResponseWriter, req *http.Request) {
	if !r.loggedIn(req) {
		http.Error(w, "Sign in first.", 401)
		return
	}
	id := safeID(req.PathValue("id"))
	b, err := os.ReadFile(filepath.Join(r.reportsDir(), id+".html"))
	if err != nil {
		http.Error(w, "No such report.", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
}

// uiDeviceRemove deletes a device's reports and drops any live connection (logged-in).
func (r *Relay) uiDeviceRemove(w http.ResponseWriter, req *http.Request) {
	var in struct{ ID string }
	if readJSON(req, &in, 2048) != nil || in.ID == "" {
		fail(w, 400, "bad request")
		return
	}
	id := safeID(in.ID)
	r.mu.Lock()
	r.deleteReportFiles(id)
	delete(r.reports, id)
	delete(r.agents, id)
	r.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) reportList() []*reportMeta {
	var out []*reportMeta
	for _, m := range r.reports {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}
