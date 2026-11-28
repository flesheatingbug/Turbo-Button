// turbo-relay: lets the Turbo Button phone app reach agents that aren't on the same network.
//
// Agents connect OUT to the relay (no open ports at the PC). The phone talks to the relay, and the
// relay passes messages between the two. Every message is end-to-end encrypted with a key that only
// the phone and the agent have (it comes from the PC's QR code), so the relay can't read or change
// them. The relay itself is protected by: an agent key (only your agents can register), paired
// phones (only phones you paired from the web page can send), and a web page with password + OTP.
package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var indexHTML []byte

var Version = "dev"

const (
	maxBlob       = 16 << 20 // one encrypted request/response
	agentStale    = 70 * time.Second
	replyTimeout  = 90 * time.Second // under Cloudflare's 100 s limit
	pollTimeout   = 25 * time.Second
	sessionMaxAge = 12 * time.Hour
	pairTTL       = 10 * time.Minute
)

// ---------- stored config ----------

type Phone struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"lastSeen"`
}

type Config struct {
	PassSalt string   `json:"passSalt"`
	PassHash string   `json:"passHash"`
	TOTP     string   `json:"totp"`
	AgentKey string   `json:"agentKey"`
	Phones   []*Phone `json:"phones"`
}

// ---------- live state ----------

type agentConn struct {
	ID, Name, OS, Mode, Build, IP string
	Since, Seen                   time.Time
	inbox                         chan relayMsg
	pending                       map[string]chan string
}

type relayMsg struct {
	RID  string `json:"rid"`
	Blob string `json:"blob"`
}

type Relay struct {
	mu       sync.Mutex
	dir      string
	cfg      Config
	agents   map[string]*agentConn
	sessions map[string]time.Time // web page logins
	pairs    map[string]time.Time // one-time phone pairing codes
	fails    map[string][]time.Time
	lastStep int64  // last TOTP step used (no replays)
	setup    string // one-time code needed to set up a fresh relay
	pendTOTP string // TOTP secret waiting for confirmation during setup
	pendPass [2]string
	trust    bool // trust X-Forwarded-* from the reverse proxy
}

func main() {
	addr := envOr("LISTEN", ":8080")
	r := &Relay{dir: envOr("CONFIG_DIR", "/config"), agents: map[string]*agentConn{}, sessions: map[string]time.Time{},
		pairs: map[string]time.Time{}, fails: map[string][]time.Time{}, trust: os.Getenv("TRUST_PROXY") != "0"}
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		log.Fatalf("can't create %s: %v", r.dir, err)
	}
	if f, err := os.CreateTemp(r.dir, ".write-test-*"); err != nil {
		log.Fatalf("can't write to %s (%v). Create the folder on the host and give it to PUID/PGID: mkdir -p <appdata>/turbo-relay && chown <PUID>:<PGID> <appdata>/turbo-relay", r.dir, err)
	} else {
		f.Close()
		os.Remove(f.Name())
	}
	r.load()
	if r.cfg.PassHash == "" {
		r.setup = randHex(4)
		log.Printf("New relay. Open the web page and use this setup code: %s", r.setup)
	}
	if r.cfg.AgentKey == "" {
		r.cfg.AgentKey = randHex(16)
		r.save()
	}
	go r.reaper()
	log.Printf("turbo-relay %s listening on %s (config in %s)", Version, addr, r.dir)
	srv := &http.Server{Addr: addr, Handler: r.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func (r *Relay) cfgPath() string { return filepath.Join(r.dir, "relay.json") }

func (r *Relay) load() {
	b, err := os.ReadFile(r.cfgPath())
	if err == nil {
		if err := json.Unmarshal(b, &r.cfg); err != nil {
			log.Fatalf("%s is damaged: %v", r.cfgPath(), err)
		}
	}
}

func (r *Relay) save() {
	b, _ := json.MarshalIndent(r.cfg, "", "  ")
	tmp := r.cfgPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err == nil {
		os.Rename(tmp, r.cfgPath())
	} else {
		log.Printf("can't save config: %v", err)
	}
}

func (r *Relay) reaper() {
	for range time.Tick(15 * time.Second) {
		r.mu.Lock()
		now := time.Now()
		for id, a := range r.agents {
			if now.Sub(a.Seen) > agentStale {
				delete(r.agents, id)
			}
		}
		for s, exp := range r.sessions {
			if now.After(exp) {
				delete(r.sessions, s)
			}
		}
		for c, exp := range r.pairs {
			if now.After(exp) {
				delete(r.pairs, c)
			}
		}
		r.mu.Unlock()
	}
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any, limit int64) error {
	return json.NewDecoder(io.LimitReader(r.Body, limit)).Decode(v)
}

func (r *Relay) clientIP(req *http.Request) string {
	if r.trust {
		if v := req.Header.Get("X-Real-IP"); v != "" {
			return strings.TrimSpace(v)
		}
		if v := req.Header.Get("X-Forwarded-For"); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
	}
	h, _, _ := net.SplitHostPort(req.RemoteAddr)
	return h
}

func (r *Relay) publicURL(req *http.Request) string {
	if u := os.Getenv("PUBLIC_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	scheme := "http"
	if req.TLS != nil || (r.trust && strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	host := req.Host
	if r.trust && req.Header.Get("X-Forwarded-Host") != "" {
		host = req.Header.Get("X-Forwarded-Host")
	}
	return scheme + "://" + host
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// tooMany reports whether ip has failed too often recently (5 in 15 minutes), and records nothing.
func (r *Relay) tooMany(ip string) bool {
	cut := time.Now().Add(-15 * time.Minute)
	var keep []time.Time
	for _, t := range r.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	r.fails[ip] = keep
	return len(keep) >= 5
}

func (r *Relay) failed(ip string) { r.fails[ip] = append(r.fails[ip], time.Now()) }

// ---------- password + TOTP ----------

func hashPass(pass, saltHex string) string {
	salt, _ := hex.DecodeString(saltHex)
	k, _ := pbkdf2.Key(sha256.New, pass, salt, 600000, 32)
	return hex.EncodeToString(k)
}

func totpAt(secret string, step int64) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	s := m.Sum(nil)
	o := s[len(s)-1] & 0xf
	v := (binary.BigEndian.Uint32(s[o:o+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v)
}

// checkTOTP accepts the current code or one 30 s step either side, and never the same step twice.
func (r *Relay) checkTOTP(secret, code string, last *int64) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	now := time.Now().Unix() / 30
	for _, s := range []int64{now, now - 1, now + 1} {
		if s > *last && eq(totpAt(secret, s), code) {
			*last = s
			return true
		}
	}
	return false
}

// ---------- routes ----------

func (r *Relay) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) { // anything else: say clearly it's the relay
		fail(w, 404, "turbo-relay: nothing at "+req.Method+" "+req.URL.Path)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) { w.Write([]byte("ok")) })

	// web page
	mux.HandleFunc("GET /ui/state", r.ui(r.uiState, false))
	mux.HandleFunc("POST /ui/setup", r.uiSetup)
	mux.HandleFunc("POST /ui/setup/confirm", r.uiSetupConfirm)
	mux.HandleFunc("POST /ui/login", r.uiLogin)
	mux.HandleFunc("POST /ui/logout", r.ui(r.uiLogout, true))
	mux.HandleFunc("POST /ui/pair", r.ui(r.uiPair, true))
	mux.HandleFunc("POST /ui/phones/{id}/revoke", r.ui(r.uiRevoke, true))
	mux.HandleFunc("POST /ui/agentkey", r.ui(r.uiRotate, true))
	mux.HandleFunc("GET /ui/download", r.uiDownload)
	mux.HandleFunc("GET /ui/rescue", r.uiRescue)
	mux.HandleFunc("POST /ui/password", r.ui(r.uiPassword, true))

	// phone app
	mux.HandleFunc("POST /phone/pair", r.phonePair)
	mux.HandleFunc("GET /phone/pcs", r.phone(r.phonePCs))
	mux.HandleFunc("POST /phone/send/{id}", r.phone(r.phoneSend))

	// agents
	mux.HandleFunc("POST /agent/hello", r.agent(r.agentHello))
	mux.HandleFunc("GET /agent/next", r.agent(r.agentNext))
	mux.HandleFunc("POST /agent/reply", r.agent(r.agentReply))
	mux.HandleFunc("POST /agent/bye", r.agent(r.agentBye))
	return mux
}

// ---------- web page handlers ----------

func (r *Relay) loggedIn(req *http.Request) bool {
	c, err := req.Cookie("tr_session")
	if err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	exp, ok := r.sessions[sha(c.Value)]
	return ok && time.Now().Before(exp)
}

// ui wraps page handlers: login required, and POSTs must carry the page's own header (CSRF guard).
func (r *Relay) ui(h http.HandlerFunc, post bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "GET" && !r.loggedIn(req) {
			r.mu.Lock()
			st := map[string]any{"loggedIn": false, "needsSetup": r.cfg.PassHash == "", "version": Version}
			r.mu.Unlock()
			writeJSON(w, 200, st)
			return
		}
		if post && req.Header.Get("X-Turbo") != "1" {
			fail(w, 403, "missing header")
			return
		}
		if !r.loggedIn(req) {
			fail(w, 401, "please sign in")
			return
		}
		h(w, req)
	}
}

func (r *Relay) setCookie(w http.ResponseWriter, req *http.Request, val string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "tr_session", Value: val, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: strings.HasPrefix(r.publicURL(req), "https://"), SameSite: http.SameSiteStrictMode})
}

func (r *Relay) uiSetup(w http.ResponseWriter, req *http.Request) {
	var in struct{ Code, Password string }
	if req.Header.Get("X-Turbo") != "1" || readJSON(req, &in, 4096) != nil {
		fail(w, 400, "bad request")
		return
	}
	ip := r.clientIP(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.PassHash != "" {
		fail(w, 409, "already set up")
		return
	}
	if r.tooMany(ip) {
		fail(w, 429, "too many attempts, wait 15 minutes")
		return
	}
	if !eq(strings.TrimSpace(strings.ToLower(in.Code)), r.setup) {
		r.failed(ip)
		fail(w, 403, "wrong setup code (see the container log: docker logs turbo-relay)")
		return
	}
	if in.Password == "" {
		fail(w, 400, "choose a password")
		return
	}
	sec := make([]byte, 20)
	rand.Read(sec)
	r.pendTOTP = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sec)
	salt := randHex(16)
	r.pendPass = [2]string{salt, hashPass(in.Password, salt)}
	uri := "otpauth://totp/Turbo%20Button%20Relay:admin?secret=" + r.pendTOTP + "&issuer=Turbo%20Button%20Relay"
	q, _ := EncodeQR(uri)
	writeJSON(w, 200, map[string]any{"secret": r.pendTOTP, "qr": q.SVG()})
}

func (r *Relay) uiSetupConfirm(w http.ResponseWriter, req *http.Request) {
	var in struct{ OTP string }
	if req.Header.Get("X-Turbo") != "1" || readJSON(req, &in, 1024) != nil {
		fail(w, 400, "bad request")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg.PassHash != "" || r.pendTOTP == "" {
		fail(w, 409, "start the setup again")
		return
	}
	var last int64
	if !r.checkTOTP(r.pendTOTP, in.OTP, &last) {
		fail(w, 403, "that code didn't match — check the phone's clock and try the next code")
		return
	}
	r.cfg.TOTP, r.cfg.PassSalt, r.cfg.PassHash = r.pendTOTP, r.pendPass[0], r.pendPass[1]
	r.lastStep, r.pendTOTP, r.setup = last, "", ""
	r.save()
	sid := randHex(32)
	r.sessions[sha(sid)] = time.Now().Add(sessionMaxAge)
	r.setCookie(w, req, sid, int(sessionMaxAge.Seconds()))
	log.Printf("setup finished from %s", r.clientIP(req))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) uiLogin(w http.ResponseWriter, req *http.Request) {
	var in struct{ Password, OTP string }
	if req.Header.Get("X-Turbo") != "1" || readJSON(req, &in, 4096) != nil {
		fail(w, 400, "bad request")
		return
	}
	ip := r.clientIP(req)
	r.mu.Lock()
	if r.cfg.PassHash == "" {
		r.mu.Unlock()
		fail(w, 409, "not set up yet")
		return
	}
	if r.tooMany(ip) {
		r.mu.Unlock()
		fail(w, 429, "too many attempts, wait 15 minutes")
		return
	}
	salt, want := r.cfg.PassSalt, r.cfg.PassHash
	r.mu.Unlock()
	passOK := eq(hashPass(in.Password, salt), want) // slow on purpose; outside the lock
	r.mu.Lock()
	defer r.mu.Unlock()
	if !passOK || !r.checkTOTP(r.cfg.TOTP, in.OTP, &r.lastStep) {
		r.failed(ip)
		log.Printf("failed sign-in from %s", ip)
		time.Sleep(time.Second)
		fail(w, 403, "wrong password or code")
		return
	}
	sid := randHex(32)
	r.sessions[sha(sid)] = time.Now().Add(sessionMaxAge)
	r.setCookie(w, req, sid, int(sessionMaxAge.Seconds()))
	log.Printf("signed in from %s", ip)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) uiLogout(w http.ResponseWriter, req *http.Request) {
	if c, err := req.Cookie("tr_session"); err == nil {
		r.mu.Lock()
		delete(r.sessions, sha(c.Value))
		r.mu.Unlock()
	}
	r.setCookie(w, req, "", -1)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) uiState(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	type ag struct {
		Name, OS, Mode, Build, IP string
		Since, Seen               time.Time
	}
	var agents []ag
	for _, a := range r.agents {
		agents = append(agents, ag{a.Name, a.OS, a.Mode, a.Build, a.IP, a.Since, a.Seen})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	writeJSON(w, 200, map[string]any{"loggedIn": true, "version": Version, "publicURL": r.publicURL(req),
		"agentKey": r.cfg.AgentKey, "agents": agents, "phones": r.cfg.Phones})
}

func (r *Relay) uiPair(w http.ResponseWriter, req *http.Request) {
	code := randHex(16)
	r.mu.Lock()
	r.pairs[sha(code)] = time.Now().Add(pairTTL)
	r.mu.Unlock()
	link := r.publicURL(req) + "/#pair=" + code
	q, err := EncodeQR(link)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"link": link, "qr": q.SVG(), "expiresMin": int(pairTTL.Minutes())})
}

func (r *Relay) uiRevoke(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	r.mu.Lock()
	defer r.mu.Unlock()
	var keep []*Phone
	for _, p := range r.cfg.Phones {
		if p.ID != id {
			keep = append(keep, p)
		}
	}
	r.cfg.Phones = keep
	r.save()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) uiRotate(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.AgentKey = randHex(16)
	r.agents = map[string]*agentConn{} // everyone has to come back with the new key
	r.save()
	writeJSON(w, 200, map[string]string{"agentKey": r.cfg.AgentKey})
}

func (r *Relay) uiPassword(w http.ResponseWriter, req *http.Request) {
	var in struct{ Current, New, OTP string }
	if readJSON(req, &in, 4096) != nil {
		fail(w, 400, "bad request")
		return
	}
	ip := r.clientIP(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tooMany(ip) {
		fail(w, 429, "too many attempts, wait 15 minutes")
		return
	}
	if !eq(hashPass(in.Current, r.cfg.PassSalt), r.cfg.PassHash) || !r.checkTOTP(r.cfg.TOTP, in.OTP, &r.lastStep) {
		r.failed(ip)
		fail(w, 403, "wrong current password or code")
		return
	}
	if in.New == "" {
		fail(w, 400, "choose a new password")
		return
	}
	r.cfg.PassSalt = randHex(16)
	r.cfg.PassHash = hashPass(in.New, r.cfg.PassSalt)
	r.save()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- phone handlers ----------

func (r *Relay) phonePair(w http.ResponseWriter, req *http.Request) {
	var in struct{ Code, Name string }
	if readJSON(req, &in, 4096) != nil {
		fail(w, 400, "bad request")
		return
	}
	ip := r.clientIP(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tooMany(ip) {
		fail(w, 429, "too many attempts, wait 15 minutes")
		return
	}
	h := sha(strings.TrimSpace(in.Code))
	exp, ok := r.pairs[h]
	if !ok || time.Now().After(exp) {
		r.failed(ip)
		fail(w, 403, "that pairing code has expired or was already used — make a new one on the relay's web page")
		return
	}
	delete(r.pairs, h)
	tok := randHex(32)
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		name = "Phone"
	}
	r.cfg.Phones = append(r.cfg.Phones, &Phone{ID: randHex(6), Name: name, TokenHash: sha(tok), Created: time.Now(), LastSeen: time.Now()})
	r.save()
	log.Printf("phone %q paired from %s", name, ip)
	writeJSON(w, 200, map[string]string{"token": tok, "relay": r.publicURL(req)})
}

func (r *Relay) phone(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		hs := sha(tok)
		r.mu.Lock()
		var found *Phone
		for _, p := range r.cfg.Phones {
			if eq(p.TokenHash, hs) {
				found = p
			}
		}
		if found != nil && time.Since(found.LastSeen) > 10*time.Minute {
			found.LastSeen = time.Now()
			r.save()
		}
		r.mu.Unlock()
		if found == nil || tok == "" {
			fail(w, 401, "this phone isn't paired with the relay (or was removed)")
			return
		}
		h(w, req)
	}
}

func (r *Relay) phonePCs(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]any{}
	for _, a := range r.agents {
		out = append(out, map[string]any{"id": a.ID, "name": a.Name, "os": a.OS, "mode": a.Mode, "since": a.Since})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"]) })
	writeJSON(w, 200, out)
}

func (r *Relay) phoneSend(w http.ResponseWriter, req *http.Request) {
	var in relayMsg
	if readJSON(req, &in, maxBlob) != nil || in.Blob == "" {
		fail(w, 400, "bad request")
		return
	}
	id := req.PathValue("id")
	r.mu.Lock()
	a := r.agents[id]
	if a == nil {
		r.mu.Unlock()
		fail(w, 404, "that PC isn't connected to the relay")
		return
	}
	rid := randHex(8)
	ch := make(chan string, 1)
	a.pending[rid] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(a.pending, rid); r.mu.Unlock() }()
	select {
	case a.inbox <- relayMsg{RID: rid, Blob: in.Blob}:
	default:
		fail(w, 503, "the PC is busy, try again")
		return
	}
	select {
	case reply := <-ch:
		writeJSON(w, 200, relayMsg{RID: rid, Blob: reply})
	case <-time.After(replyTimeout):
		fail(w, 504, "the PC didn't answer in time")
	case <-req.Context().Done():
	}
}

// ---------- agent handlers ----------

func (r *Relay) agent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		key := r.cfg.AgentKey
		r.mu.Unlock()
		if !eq(req.Header.Get("X-Agent-Key"), key) {
			time.Sleep(500 * time.Millisecond)
			fail(w, 401, "wrong relay key")
			return
		}
		h(w, req)
	}
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (r *Relay) agentHello(w http.ResponseWriter, req *http.Request) {
	var in struct{ ID, Name, OS, Mode, Build string }
	if readJSON(req, &in, 4096) != nil || !validID(in.ID) {
		fail(w, 400, "bad request")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.agents[in.ID]
	if a == nil {
		if len(r.agents) >= 500 {
			fail(w, 503, "too many PCs connected")
			return
		}
		a = &agentConn{ID: in.ID, Since: time.Now(), inbox: make(chan relayMsg, 64), pending: map[string]chan string{}}
		r.agents[in.ID] = a
		log.Printf("PC connected: %q (%s, %s)", clip(in.Name, 60), clip(in.OS, 20), clip(in.Mode, 20))
	}
	a.Name, a.OS, a.Mode, a.Build, a.IP, a.Seen = clip(in.Name, 60), clip(in.OS, 20), clip(in.Mode, 20), clip(in.Build, 20), r.clientIP(req), time.Now()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (r *Relay) agentNext(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	r.mu.Lock()
	a := r.agents[id]
	if a != nil {
		a.Seen = time.Now()
	}
	r.mu.Unlock()
	if a == nil {
		fail(w, 404, "say hello first")
		return
	}
	select {
	case m := <-a.inbox:
		writeJSON(w, 200, m)
	case <-time.After(pollTimeout):
		w.WriteHeader(204)
	case <-req.Context().Done():
	}
	r.mu.Lock()
	a.Seen = time.Now()
	r.mu.Unlock()
}

func (r *Relay) agentReply(w http.ResponseWriter, req *http.Request) {
	var in struct{ ID, RID, Blob string }
	if readJSON(req, &in, maxBlob) != nil {
		fail(w, 400, "bad request")
		return
	}
	r.mu.Lock()
	a := r.agents[in.ID]
	var ch chan string
	if a != nil {
		ch = a.pending[in.RID]
		a.Seen = time.Now()
	}
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- in.Blob:
		default:
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": ch != nil})
}

func (r *Relay) agentBye(w http.ResponseWriter, req *http.Request) {
	var in struct{ ID string }
	readJSON(req, &in, 1024)
	r.mu.Lock()
	if a := r.agents[in.ID]; a != nil {
		delete(r.agents, in.ID)
		log.Printf("PC disconnected: %q", a.Name)
	}
	r.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
