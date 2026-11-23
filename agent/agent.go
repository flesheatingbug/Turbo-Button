package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed scripts/*.ps1
var scriptFS embed.FS

//go:embed web/index.html
var indexHTML []byte

const Version = "0.3.0"

type Job struct {
	ID       int             `json:"id"`
	Action   string          `json:"action"`
	Title    string          `json:"title"`
	Params   map[string]any  `json:"params"`
	Reason   string          `json:"reason,omitempty"`
	Status   string          `json:"status"` // queued running done failed cancelled
	Queued   time.Time       `json:"queued"`
	Started  *time.Time      `json:"started,omitempty"`
	Finished *time.Time      `json:"finished,omitempty"`
	Output   string          `json:"output"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Auto     bool            `json:"auto,omitempty"` // queued by the agent itself
	env      map[string]string
	modifies bool
	timeout  time.Duration
}

// Event is what the console log (and anything else) sees as jobs progress.
type Event struct {
	Kind string // queued start line done failed phone
	Job  *Job
	Text string
}

type Agent struct {
	mu           sync.Mutex
	token        string
	jobs         []*Job
	nextID       int
	queue        chan *Job
	workDir      string
	lastScan     json.RawMessage
	firstScan    json.RawMessage
	autoRestore  bool
	restoreTried bool
	ctx          context.Context
	cancel       context.CancelFunc
	finishing    chan struct{}
	finishOnce   sync.Once
	lastSeen     time.Time
	started      time.Time
	phoneSeen    bool
	OnEvent      func(Event)
}

func (a *Agent) emit(e Event) {
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
}

func NewAgent() (*Agent, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	wd, err := os.MkdirTemp("", "turbobutton-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{token: hex.EncodeToString(b), queue: make(chan *Job, 256), workDir: wd, autoRestore: true,
		ctx: ctx, cancel: cancel, finishing: make(chan struct{}), started: time.Now(), lastSeen: time.Now()}
	go a.worker()
	return a, nil
}

// Cleanup removes everything the agent created on disk.
func (a *Agent) Cleanup() {
	a.cancel()
	os.RemoveAll(a.workDir)
}

func (a *Agent) Finish() { a.finishOnce.Do(func() { close(a.finishing) }) }

func (a *Agent) enqueue(actionID string, params map[string]any, reason string, auto bool) (*Job, error) {
	title := "Scan this PC"
	env := map[string]string{}
	modifies := false
	timeout := 15 * time.Minute
	if actionID != "scan" {
		act := findAction(actionID)
		if act == nil {
			return nil, fmt.Errorf("unknown action %q", actionID)
		}
		if params == nil {
			params = map[string]any{}
		}
		vals, err := ValidateParams(act, params)
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			env["PCM_"+k] = v
		}
		title, modifies = act.Title, act.Modifies
		timeout = time.Duration(act.TimeoutMin) * time.Minute
		if timeout == 0 {
			timeout = 30 * time.Minute
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) >= cap(a.queue)-1 {
		return nil, errors.New("queue is full; wait for jobs to finish")
	}
	a.nextID++
	j := &Job{ID: a.nextID, Action: actionID, Title: title, Params: params, Reason: reason, Status: "queued",
		Queued: time.Now(), env: env, modifies: modifies, Auto: auto, timeout: timeout}
	a.jobs = append(a.jobs, j)
	a.queue <- j
	go a.emit(Event{Kind: "queued", Job: j})
	return j, nil
}

func (a *Agent) worker() {
	for {
		var j *Job
		select {
		case <-a.ctx.Done():
			return
		case j = <-a.queue:
		}
		a.mu.Lock()
		cancelled := j.Status == "cancelled"
		needRestore := j.modifies && a.autoRestore && !a.restoreTried && j.Action != "restore_point"
		if needRestore {
			a.restoreTried = true
		}
		a.mu.Unlock()
		if cancelled {
			continue
		}
		if needRestore {
			a.mu.Lock()
			a.nextID++
			rp := &Job{ID: a.nextID, Action: "restore_point", Title: findAction("restore_point").Title, Params: map[string]any{},
				Reason: "Automatic safety restore point before the first change", Status: "queued", Queued: time.Now(), Auto: true, env: map[string]string{}, timeout: 15 * time.Minute}
			// keep it listed before the job that triggered it
			for i, x := range a.jobs {
				if x == j {
					a.jobs = append(a.jobs[:i], append([]*Job{rp}, a.jobs[i:]...)...)
					break
				}
			}
			a.mu.Unlock()
			a.run(rp)
		}
		if j.Action == "restore_point" {
			a.mu.Lock()
			a.restoreTried = true
			a.mu.Unlock()
		}
		a.run(j)
	}
}

func (a *Agent) run(j *Job) {
	now := time.Now()
	a.mu.Lock()
	j.Status, j.Started = "running", &now
	a.mu.Unlock()
	a.emit(Event{Kind: "start", Job: j})

	name := j.Action
	res, err := a.runScript(name, j.env, j.timeout, func(line string) {
		a.mu.Lock()
		j.Output += line + "\n"
		if len(j.Output) > 12000 {
			j.Output = j.Output[len(j.Output)-12000:]
		}
		a.mu.Unlock()
		a.emit(Event{Kind: "line", Job: j, Text: line})
	})
	end := time.Now()
	a.mu.Lock()
	j.Finished = &end
	if err != nil {
		j.Status, j.Error = "failed", err.Error()
		a.mu.Unlock()
		a.emit(Event{Kind: "failed", Job: j, Text: err.Error()})
		return
	}
	j.Status, j.Result = "done", res
	defer a.emit(Event{Kind: "done", Job: j})
	defer a.mu.Unlock()
	if name == "scan" {
		a.lastScan = res
		if a.firstScan == nil {
			a.firstScan = res
		}
	}
}

func (a *Agent) runScript(name string, env map[string]string, timeout time.Duration, onLine func(string)) (json.RawMessage, error) {
	prelude, _ := scriptFS.ReadFile("scripts/_prelude.ps1")
	body, err := scriptFS.ReadFile("scripts/" + name + ".ps1")
	if err != nil {
		return nil, fmt.Errorf("no script for %s", name)
	}
	if mockMode() {
		return mockRun(name, env, onLine)
	}
	path := filepath.Join(a.workDir, fmt.Sprintf("%s-%d.ps1", name, time.Now().UnixNano()))
	// UTF-8 with BOM so Windows PowerShell 5.1 reads it as UTF-8.
	content := append([]byte{0xEF, 0xBB, 0xBF}, prelude...)
	content = append(content, '\n')
	content = append(content, body...)
	if err := os.WriteFile(path, content, 0600); err != nil {
		return nil, err
	}
	defer os.Remove(path)

	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	hideWindow(cmd)
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var result json.RawMessage
	var scriptErr string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.HasPrefix(line, "##RESULT##"):
			result = json.RawMessage(strings.TrimPrefix(line, "##RESULT##"))
		case strings.HasPrefix(line, "##ERROR##"):
			scriptErr = strings.TrimPrefix(line, "##ERROR##")
		case strings.TrimSpace(line) != "":
			onLine(line)
		}
	}
	werr := cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("stopped after %v (time limit)", timeout)
	}
	if scriptErr != "" {
		return nil, errors.New(scriptErr)
	}
	if werr != nil && result == nil {
		return nil, fmt.Errorf("script failed: %v", werr)
	}
	if result == nil {
		return nil, errors.New("script finished without a result")
	}
	if !json.Valid(result) {
		return nil, errors.New("script returned invalid JSON")
	}
	return result, nil
}

// ---------- HTTP ----------

func (a *Agent) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := r.Header.Get("X-Token")
		if subtle.ConstantTimeCompare([]byte(t), []byte(a.token)) != 1 {
			http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
			return
		}
		a.mu.Lock()
		first := !a.phoneSeen
		a.lastSeen, a.phoneSeen = time.Now(), true
		a.mu.Unlock()
		if first {
			a.emit(Event{Kind: "phone"})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
func writeErr(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	writeJSON(w, map[string]string{"error": msg})
}

func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/info", a.authed(func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		a.mu.Lock()
		defer a.mu.Unlock()
		writeJSON(w, map[string]any{"agent": "Turbo Button", "version": Version, "computer": host, "catalog": Catalog,
			"autoRestorePoint": a.autoRestore, "restorePointDone": a.restoreTried, "startedAt": a.started,
			"protectedServices": protectedServiceList(), "protectedAppPrefixes": protectedAppPrefixes})
	}))
	mux.HandleFunc("POST /api/settings", a.authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AutoRestorePoint *bool `json:"autoRestorePoint"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
		a.mu.Lock()
		if in.AutoRestorePoint != nil {
			a.autoRestore = *in.AutoRestorePoint
		}
		v := a.autoRestore
		a.mu.Unlock()
		writeJSON(w, map[string]any{"autoRestorePoint": v})
	}))
	mux.HandleFunc("POST /api/scan", a.authed(func(w http.ResponseWriter, r *http.Request) {
		j, err := a.enqueue("scan", nil, "", false)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, j)
	}))
	mux.HandleFunc("GET /api/scan", a.authed(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		writeJSON(w, map[string]any{"latest": a.lastScan, "first": a.firstScan})
	}))
	mux.HandleFunc("POST /api/apply", a.authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string         `json:"action"`
			Params map[string]any `json:"params"`
			Reason string         `json:"reason"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			writeErr(w, 400, "bad JSON")
			return
		}
		if in.Action == "scan" {
			writeErr(w, 400, "use /api/scan")
			return
		}
		j, err := a.enqueue(in.Action, in.Params, in.Reason, false)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, j)
	}))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", a.authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, j := range a.jobs {
			if fmt.Sprint(j.ID) == id && j.Status == "queued" {
				j.Status = "cancelled"
				writeJSON(w, j)
				return
			}
		}
		writeErr(w, 400, "only queued jobs can be cancelled")
	}))
	mux.HandleFunc("GET /api/jobs", a.authed(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.jobs == nil {
			writeJSON(w, []*Job{})
			return
		}
		writeJSON(w, a.jobs)
	}))
	mux.HandleFunc("POST /api/finish", a.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "message": "Session ended. The agent is cleaning up and closing."})
		go func() { time.Sleep(500 * time.Millisecond); a.Finish() }()
	}))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func protectedServiceList() []string {
	out := make([]string, 0, len(protectedServices))
	for k := range protectedServices {
		out = append(out, k)
	}
	return out
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
