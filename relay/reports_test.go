package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportsStoreListRemove(t *testing.T) {
	r := &Relay{dir: t.TempDir(), reports: map[string]*reportMeta{}, agents: map[string]*agentConn{}, sessions: map[string]time.Time{sha("s"): time.Now().Add(time.Hour)}}
	// store
	w := httptest.NewRecorder()
	r.phoneReport(w, httptest.NewRequest("POST", "/phone/report", strings.NewReader(`{"id":"dev1","name":"PC-A","os":"Windows","summary":"cleaned up","html":"<h1>hi</h1>"}`)))
	if w.Code != 200 {
		t.Fatalf("store: %d %s", w.Code, w.Body)
	}
	if len(r.reportList()) != 1 || r.reportList()[0].Name != "PC-A" {
		t.Fatalf("list: %+v", r.reportList())
	}
	// view (logged in via cookie s)
	vw := httptest.NewRecorder()
	vreq := httptest.NewRequest("GET", "/ui/report/dev1", nil)
	vreq.AddCookie(&http.Cookie{Name: "tr_session", Value: "s"})
	vreq.SetPathValue("id", "dev1")
	r.uiReport(vw, vreq)
	if vw.Code != 200 || !strings.Contains(vw.Body.String(), "hi") {
		t.Fatalf("view: %d %s", vw.Code, vw.Body)
	}
	// reject bad id traversal
	if safeID("../../etc/passwd") != "etcpasswd" {
		t.Fatalf("safeID: %q", safeID("../../etc/passwd"))
	}
	// remove
	rw := httptest.NewRecorder()
	rreq := httptest.NewRequest("POST", "/ui/device/remove", strings.NewReader(`{"id":"dev1"}`))
	rreq.AddCookie(&http.Cookie{Name: "tr_session", Value: "s"})
	r.uiDeviceRemove(rw, rreq)
	if rw.Code != 200 || len(r.reportList()) != 0 {
		t.Fatalf("remove: %d, left %d", rw.Code, len(r.reportList()))
	}
}
