package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDownload(t *testing.T) {
	exe, app := []byte("MZ pretend windows program"), []byte("\x7fELF pretend appimage")
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/SHA256SUMS":
			io.WriteString(w, sum(exe)+"  TurboButton.exe\n"+sum(app)+"  TurboButton-x86_64.AppImage\n")
		case "/TurboButton.exe":
			w.Write(exe)
		case "/TurboButton-x86_64.AppImage":
			w.Write(app)
		default:
			http.NotFound(w, q)
		}
	}))
	defer gh.Close()
	t.Setenv("RELEASE_URL", gh.URL)
	t.Setenv("PUBLIC_URL", "https://turbo.example")
	r := &Relay{dir: t.TempDir(), sessions: map[string]time.Time{sha("s1"): time.Now().Add(time.Hour)}}
	r.cfg.AgentKey = "k123"
	get := func(q string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/ui/download?"+q, nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "tr_session", Value: "s1"})
		}
		w := httptest.NewRecorder()
		r.uiDownload(w, req)
		return w
	}
	if w := get("os=windows", false); w.Code != 401 {
		t.Fatalf("not signed in: %d", w.Code)
	}
	w := get("os=windows&standby=1", true)
	b := w.Body.Bytes()
	if w.Code != 200 || !bytes.HasPrefix(b, exe) || !bytes.HasSuffix(b, []byte(confMagic)) || !strings.Contains(string(b), "relaykey=k123\nstandby=yes") {
		t.Fatalf("windows: %d %q", w.Code, b)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "TurboButton-office.exe") {
		t.Fatal(w.Header())
	}
	w = get("os=linux", true)
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		c, _ := io.ReadAll(rc)
		t.Logf("%s %v %q", f.Name, f.Mode(), c)
	}
	if len(zr.File) != 2 || zr.File[0].Mode()&0100 == 0 {
		t.Fatal("zip contents")
	}
}
