package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRescueMakerZip(t *testing.T) {
	r := &Relay{dir: t.TempDir(), sessions: map[string]time.Time{sha("s"): time.Now().Add(time.Hour)}}
	r.cfg.AgentKey = "k1"
	t.Setenv("PUBLIC_URL", "https://turbo.example")
	req := httptest.NewRequest("GET", "/ui/rescue", nil)
	req.AddCookie(&http.Cookie{Name: "tr_session", Value: "s"})
	w := httptest.NewRecorder()
	r.uiRescue(w, req)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("code %d ct %s", w.Code, w.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"turbo-rescue/make-rescue-usb.sh", "turbo-rescue/make-rescue-usb.ps1", "turbo-rescue/turbobutton.conf"} {
		if !names[want] {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	// conf has the relay baked in
	for _, f := range zr.File {
		if f.Name == "turbo-rescue/turbobutton.conf" {
			rc, _ := f.Open()
			b := new(bytes.Buffer)
			b.ReadFrom(rc)
			if !strings.Contains(b.String(), "relaykey=k1") {
				t.Fatalf("conf: %s", b.String())
			}
		}
	}
}
