package main

// Relay mode: the agent connects OUT to the technician's own turbo-relay server, so the phone can
// reach it from anywhere (remote desktop jobs, office PCs). Every message is end-to-end encrypted
// with a key derived from the session token, which only the phone (from the QR code) and this agent
// know: the relay just passes sealed boxes along. Requests are replayed into the normal HTTP handler,
// so everything works exactly as it does on the local network.

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type relayConf struct {
	URL      string `json:"url,omitempty"`
	Key      string `json:"key,omitempty"`
	Standby  bool   `json:"standby,omitempty"`
	Embedded bool   `json:"-"` // settings came built into the program
}

func (c relayConf) on() bool { return c.URL != "" && c.Key != "" }

const confName = "turbobutton.conf"

// confFile finds turbobutton.conf next to the AppImage / program.
func confFile() string {
	var dirs []string
	if ai := os.Getenv("APPIMAGE"); ai != "" {
		dirs = append(dirs, filepath.Dir(ai))
	}
	if exe, err := os.Executable(); err == nil {
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
		dirs = append(dirs, filepath.Dir(exe))
	}
	// Notepad saves "turbobutton.conf" as "turbobutton.conf.txt" with the .txt hidden, so accept that too.
	for _, d := range dirs {
		for _, n := range []string{confName, confName + ".txt", "turbobutton.txt"} {
			p := filepath.Join(d, n)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

func argValue(name string) (string, bool) {
	for i, x := range os.Args[1:] {
		if strings.HasPrefix(x, name+"=") {
			return strings.TrimPrefix(x, name+"="), true
		}
		if x == name && i+2 <= len(os.Args[1:]) {
			return os.Args[i+2], true
		}
	}
	return "", false
}

// Settings can be built into the program itself: the relay's download button appends them to
// TurboButton.exe as  <conf text> <uint32 length, little-endian> "TBCONF01". Windows ignores
// data after the program, and self-update copies the block onto the new build.
const confMagic = "TBCONF01"

func embeddedConf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < 12 {
		return ""
	}
	tail := make([]byte, 12)
	if _, err := f.ReadAt(tail, fi.Size()-12); err != nil || string(tail[4:]) != confMagic {
		return ""
	}
	n := int64(binary.LittleEndian.Uint32(tail[:4]))
	if n == 0 || n > 8192 || n > fi.Size()-12 {
		return ""
	}
	b := make([]byte, n)
	if _, err := f.ReadAt(b, fi.Size()-12-n); err != nil {
		return ""
	}
	return string(b)
}

// appendConf adds a settings block to the end of a program file.
func appendConf(path, conf string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	tail := make([]byte, 4)
	binary.LittleEndian.PutUint32(tail, uint32(len(conf)))
	_, err = f.Write(append(append([]byte(conf), tail...), confMagic...))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// selfPath is the file Turbo Button was started from (the AppImage itself on Linux).
func selfPath() string {
	if ai := os.Getenv("APPIMAGE"); ai != "" {
		return ai
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe
}

func parseConf(c *relayConf, text string) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff")), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "relay":
			c.URL = strings.TrimSpace(v)
		case "relaykey", "relay_key", "key":
			c.Key = strings.TrimSpace(v)
		case "standby":
			c.Standby = isYes(v)
		}
	}
}

// loadRelayConf: settings built into the program, then turbobutton.conf, then environment,
// then command-line options (last wins).
func loadRelayConf() relayConf {
	var c relayConf
	if p := selfPath(); p != "" {
		if e := embeddedConf(p); e != "" {
			parseConf(&c, e)
			c.Embedded = true
		}
	}
	if p := confFile(); p != "" {
		if b, err := os.ReadFile(p); err == nil && len(b) < 64*1024 {
			parseConf(&c, string(b))
		}
	}
	if v := os.Getenv("TURBO_RELAY"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("TURBO_RELAY_KEY"); v != "" {
		c.Key = v
	}
	if v, ok := argValue("--relay"); ok {
		c.URL = v
	}
	if v, ok := argValue("--relay-key"); ok {
		c.Key = v
	}
	if hasArg("--standby") {
		c.Standby = true
	}
	if hasArg("--no-relay") {
		c = relayConf{}
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if c.URL != "" && !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") {
		c.URL = "https://" + c.URL
	}
	return c
}

func isYes(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "yes", "true", "on":
		return true
	}
	return false
}

// ---------- end-to-end encryption (must match the phone's WebCrypto code) ----------

func e2eKey(token string) []byte {
	k := sha256.Sum256([]byte("turbo-button e2e v1|" + token))
	return k[:]
}

func relayID(token string) string {
	h := sha256.Sum256([]byte("turbo-button id v1|" + token))
	return hex.EncodeToString(h[:16])
}

func seal(key []byte, aad string, plain []byte) (string, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, plain, []byte(aad))), nil
}

func open(key []byte, aad, blob string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(blob)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, _ := cipher.NewGCM(blk)
	if len(raw) < g.NonceSize() {
		return nil, errors.New("short message")
	}
	return g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(aad))
}

type sealedReq struct {
	Method string            `json:"m"`
	Path   string            `json:"p"`
	Header map[string]string `json:"h"`
	Body   string            `json:"b"`
	TS     int64             `json:"ts"` // ms
	N      string            `json:"n"`  // unique per request
}

type sealedResp struct {
	Status int    `json:"s"`
	Body   string `json:"b"`
}

// ---------- relay client ----------

type relayClient struct {
	a      *Agent
	conf   relayConf
	h      http.Handler
	id     string
	key    []byte
	hc     *http.Client
	mu     sync.Mutex
	seen   map[string]time.Time
	status string
	stop   chan struct{}
}

func (a *Agent) startRelay(conf relayConf, h http.Handler) *relayClient {
	rc := &relayClient{a: a, conf: conf, h: h, id: relayID(a.token), key: e2eKey(a.token),
		hc: &http.Client{Timeout: 45 * time.Second}, seen: map[string]time.Time{}, stop: make(chan struct{})}
	go rc.loop()
	return rc
}

func (rc *relayClient) setStatus(s string) {
	rc.mu.Lock()
	changed := s != rc.status
	rc.status = s
	rc.mu.Unlock()
	if changed && rc.a.OnEvent != nil {
		rc.a.OnEvent(Event{Kind: "relay", Text: s})
	}
}

func (rc *relayClient) Status() string { rc.mu.Lock(); defer rc.mu.Unlock(); return rc.status }

func (rc *relayClient) post(path string, v any) (*http.Response, error) {
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest("POST", rc.conf.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Key", rc.conf.Key)
	req.Header.Set("User-Agent", "turbobutton-agent/"+Version)
	return rc.hc.Do(req)
}

func (rc *relayClient) hello() error {
	mode := "session"
	if rc.conf.Standby {
		mode = "standby"
	}
	r, err := rc.post("/agent/hello", map[string]string{"id": rc.id, "name": hostName(), "os": runtime.GOOS, "mode": mode, "build": Build})
	if err != nil {
		return errors.New("can't reach the relay")
	}
	defer r.Body.Close()
	if r.StatusCode == 401 {
		return errors.New("the relay key is wrong (check turbobutton.conf)")
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("relay answered %d", r.StatusCode)
	}
	return nil
}

func (rc *relayClient) loop() {
	wait := 2 * time.Second
	for {
		select {
		case <-rc.stop:
			return
		default:
		}
		if err := rc.hello(); err != nil {
			rc.setStatus("relay: " + err.Error() + " — retrying")
			select {
			case <-time.After(wait):
			case <-rc.stop:
				return
			}
			if wait < time.Minute {
				wait *= 2
			}
			continue
		}
		wait = 2 * time.Second
		rc.setStatus("relay: connected to " + strings.TrimPrefix(strings.TrimPrefix(rc.conf.URL, "https://"), "http://"))
		rc.pollLoop()
	}
}

// pollLoop long-polls for requests until the relay forgets us or the network drops.
func (rc *relayClient) pollLoop() {
	for {
		select {
		case <-rc.stop:
			return
		default:
		}
		req, _ := http.NewRequest("GET", rc.conf.URL+"/agent/next?id="+rc.id, nil)
		req.Header.Set("X-Agent-Key", rc.conf.Key)
		r, err := rc.hc.Do(req)
		if err != nil {
			rc.setStatus("relay: connection lost — retrying")
			time.Sleep(3 * time.Second)
			return
		}
		switch r.StatusCode {
		case 204:
			r.Body.Close()
		case 200:
			var m struct{ RID, Blob string }
			json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&m)
			r.Body.Close()
			go rc.handle(m.RID, m.Blob)
		default:
			r.Body.Close()
			return // 401/404: say hello again
		}
	}
}

func (rc *relayClient) handle(rid, blob string) {
	resp := sealedResp{Status: 400, Body: `{"error":"bad request"}`}
	plain, err := open(rc.key, rc.id, blob)
	var q sealedReq
	if err != nil || json.Unmarshal(plain, &q) != nil {
		return // not from a phone that has the key: no answer at all
	}
	now := time.Now()
	rc.mu.Lock()
	for n, t := range rc.seen {
		if now.Sub(t) > 10*time.Minute {
			delete(rc.seen, n)
		}
	}
	replay := q.N == "" || !rc.seen[q.N].IsZero()
	rc.seen[q.N] = now
	rc.mu.Unlock()
	if replay || absDur(now.Sub(time.UnixMilli(q.TS))) > 5*time.Minute || !strings.HasPrefix(q.Path, "/api/") {
		resp = sealedResp{Status: 403, Body: `{"error":"rejected (old or repeated request — check the phone's clock)"}`}
	} else {
		req := httptest.NewRequest(q.Method, q.Path, strings.NewReader(q.Body))
		for k, v := range q.Header {
			req.Header.Set(k, v)
		}
		req.RemoteAddr = "relay:0" // never counts as "local", so the customer-screen endpoints stay off-limits
		rec := httptest.NewRecorder()
		rc.h.ServeHTTP(rec, req)
		resp = sealedResp{Status: rec.Code, Body: rec.Body.String()}
	}
	b, _ := json.Marshal(resp)
	out, err := seal(rc.key, rc.id, b)
	if err != nil {
		return
	}
	if r, err := rc.post("/agent/reply", map[string]string{"id": rc.id, "rid": rid, "blob": out}); err == nil {
		r.Body.Close()
	}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (rc *relayClient) Close() {
	select {
	case <-rc.stop:
		return
	default:
	}
	close(rc.stop)
	c := &http.Client{Timeout: 3 * time.Second}
	b, _ := json.Marshal(map[string]string{"id": rc.id})
	req, _ := http.NewRequest("POST", rc.conf.URL+"/agent/bye", bytes.NewReader(b))
	req.Header.Set("X-Agent-Key", rc.conf.Key)
	if r, err := c.Do(req); err == nil {
		r.Body.Close()
	}
}

// ---------- standby: keep the same session key across runs, so the phone stays paired ----------

func standbyFile() string { return filepath.Join(resumeDir(), "standby.json") }

func loadStandbyToken() string {
	b, err := os.ReadFile(standbyFile())
	if err != nil {
		return ""
	}
	var s struct{ Token string }
	if json.Unmarshal(b, &s) != nil || len(s.Token) != 32 {
		return ""
	}
	return s.Token
}

func saveStandbyToken(tok string) {
	os.MkdirAll(resumeDir(), 0700)
	b, _ := json.Marshal(map[string]string{"token": tok})
	os.WriteFile(standbyFile(), b, 0600)
	lockDown(standbyFile())
}
