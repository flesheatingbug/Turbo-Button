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
	"net"
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

//go:embed winres/icon.png
var iconPNG []byte

//go:embed web/screen.html
var screenHTML []byte

const Version = "0.11.4"

type Job struct {
	ID        int             `json:"id"`
	Action    string          `json:"action"`
	Title     string          `json:"title"`
	Params    map[string]any  `json:"params"`
	Reason    string          `json:"reason,omitempty"`
	Status    string          `json:"status"` // queued running done failed cancelled
	Queued    time.Time       `json:"queued"`
	Started   *time.Time      `json:"started,omitempty"`
	Finished  *time.Time      `json:"finished,omitempty"`
	Output    string          `json:"output"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Auto      bool            `json:"auto,omitempty"`   // queued by the agent itself
	Held      bool            `json:"held,omitempty"`   // queued while the queue is paused
	Forced    bool            `json:"forced,omitempty"` // a system-changing job stopped halfway on purpose
	env       map[string]string
	Stoppable bool               `json:"stoppable,omitempty"`
	stop      context.CancelFunc // stops it while it runs (read-only jobs only)
	stopped   bool
	modifies  bool
	timeout   time.Duration
}

// Event is what the console log (and anything else) sees as jobs progress.
type Event struct {
	Kind string // queued start line done failed phone
	Job  *Job
	Text string
}

type Agent struct {
	relayConf    relayConf
	relay        *relayClient
	mu           sync.Mutex
	token        string
	jobs         []*Job
	nextID       int
	queue        chan *Job
	paused       bool // the technician paused the queue: the running job finishes, the next waits
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
	machineID    string
	resumed      bool
	resumeArmed  bool
	port         int
	viewToken    string
	controlURL   string
	qrSVG        string
	screenSeen   time.Time // last poll from the customer screen
	screenClosed time.Time // when the screen reported it was closed
}

func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	// Sweep leftovers from a session that never got to clean up (e.g. the PC lost power).
	if old, _ := filepath.Glob(filepath.Join(os.TempDir(), "turbobutton-*")); len(old) > 0 {
		for _, d := range old {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > 10*time.Minute {
				os.RemoveAll(d)
			}
		}
	}
	wd, err := os.MkdirTemp("", "turbobutton-")
	if err != nil {
		return nil, err
	}
	vb := make([]byte, 16)
	rand.Read(vb)
	ctx, cancel := context.WithCancel(context.Background())
	a := &Agent{token: hex.EncodeToString(b), viewToken: hex.EncodeToString(vb), queue: make(chan *Job, 256), workDir: wd, autoRestore: true,
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
		if env["PCM_mode"] == "check" && (actionID == "windows_update" || actionID == "driver_updates" || actionID == "feature_update") {
			modifies = false // only looks; can be stopped and needs no restore point
		}
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
		Queued: time.Now(), env: env, modifies: modifies, Auto: auto, timeout: timeout, Held: a.paused}
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
		// Paused: wait here (the job stays "queued") until resumed or cancelled.
		for {
			a.mu.Lock()
			p, c := a.paused, j.Status == "cancelled"
			a.mu.Unlock()
			if !p || c {
				break
			}
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
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
		switch j.Action {
		case "restart_pc", "defender_offline_scan":
			if err := a.prepareResume(a.port); err != nil {
				a.emit(Event{Kind: "line", Job: j, Text: "Could not set up auto-resume: " + err.Error() + " (run TurboButton.exe again after the restart)"})
			} else {
				a.emit(Event{Kind: "line", Job: j, Text: "Auto-resume armed: Turbo Button reopens after the restart and the phone reconnects."})
			}
		case "cancel_restart":
			cancelResume()
			a.mu.Lock()
			a.resumeArmed = false
			a.mu.Unlock()
		}
		a.run(j)
	}
}

// stoppable: read-only jobs can be stopped halfway. Anything that changes the system can't:
// killing an update, uninstall or repair halfway could leave the PC broken.
func (j *Job) stoppable() bool {
	return !j.modifies && j.Action != "restore_point" && j.Action != "restart_pc" && j.Action != "cancel_restart"
}

func (a *Agent) run(j *Job) {
	now := time.Now()
	jctx, stop := context.WithCancel(a.ctx)
	defer stop()
	a.mu.Lock()
	j.Status, j.Started = "running", &now
	j.stop = stop
	j.Stoppable = j.stoppable()
	a.mu.Unlock()
	a.emit(Event{Kind: "start", Job: j})

	name := j.Action
	res, err := a.runScript(jctx, name, j.env, j.timeout, func(line string) {
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
	j.stop = nil
	if j.stopped {
		j.Status, j.Error = "cancelled", "Stopped by the technician"
		if j.Forced {
			j.Error = "Force-stopped by the technician while it was changing the system — check it finished cleanly (rescan, or run it again)"
		}
		a.mu.Unlock()
		a.emit(Event{Kind: "failed", Job: j, Text: "stopped by the technician"})
		return
	}
	if err != nil {
		j.Status, j.Error = "failed", err.Error()
		armed := a.resumeArmed
		a.mu.Unlock()
		a.emit(Event{Kind: "failed", Job: j, Text: err.Error()})
		if armed {
			a.writeResumeState(a.port)
		}
		return
	}
	j.Status, j.Result = "done", res
	defer func() {
		if a.resumeArmed {
			a.writeResumeState(a.port)
		}
	}()
	defer a.emit(Event{Kind: "done", Job: j})
	defer a.mu.Unlock()
	if name == "scan" {
		a.lastScan = res
		if a.firstScan == nil {
			a.firstScan = res
		}
	}
}

// nativeRun handles scans and actions without PowerShell (set by the Linux build).
var nativeRun func(ctx context.Context, name string, env map[string]string, onLine func(string)) (json.RawMessage, error)

func (a *Agent) runScript(parent context.Context, name string, env map[string]string, timeout time.Duration, onLine func(string)) (json.RawMessage, error) {
	if nativeRun != nil && !mockMode() {
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		res, err := nativeRun(ctx, name, env, onLine)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("stopped after %v (time limit)", timeout)
		}
		return res, err
	}
	prelude, _ := scriptFS.ReadFile("scripts/_prelude.ps1")
	body, err := scriptFS.ReadFile("scripts/" + name + ".ps1")
	if err != nil {
		return nil, fmt.Errorf("no script for %s", name)
	}
	if mockMode() {
		return mockRun(parent, name, env, onLine)
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

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if name != "scan" {
		r := a.runPS(ctx, path, env, 0, onLine)
		return r.finish(ctx, timeout)
	}
	// The scan: if one section hangs (broken WMI, a dead network drive…), skip just that section and
	// carry on from the next one, keeping everything already collected.
	sections := map[string]json.RawMessage{}
	var notes []string
	env2 := map[string]string{}
	for k, v := range env {
		env2[k] = v
	}
	for attempt := 0; attempt < 6; attempt++ {
		r := a.runPS(ctx, path, env2, scanStall, onLine)
		for k, v := range r.sections {
			sections[k] = v
		}
		notes = append(notes, r.sectionErrs...)
		if !r.stalled {
			if r.result != nil && json.Valid(r.result) {
				if len(notes) == 0 || attempt == 0 {
					return r.result, nil
				}
				var full map[string]json.RawMessage
				if json.Unmarshal(r.result, &full) == nil {
					for k, v := range full {
						sections[k] = v
					}
				}
				break
			}
			if len(sections) == 0 {
				return r.finish(ctx, timeout)
			}
			notes = append(notes, "scan ended early: "+r.errText())
			break
		}
		msg := fmt.Sprintf("the %s check didn't answer for %s, so it was skipped", r.lastSection, strings.TrimSuffix(strings.TrimSuffix(scanStall.String(), "0s"), "m0")+map[bool]string{true: " min", false: ""}[scanStall >= time.Minute])
		onLine("⚠ " + msg + " — carrying on with the rest")
		notes = append(notes, msg)
		if r.lastSection == "" {
			break
		}
		env2["PCM_RESUME_AFTER"] = r.lastSection
	}
	errs, _ := json.Marshal(notes)
	sections["errors"] = errs
	if _, ok := sections["scannedAt"]; !ok {
		sections["scannedAt"], _ = json.Marshal(time.Now().Format("2006-01-02T15:04:05"))
	}
	return json.Marshal(sections)
}

type psRun struct {
	result      json.RawMessage
	scriptErr   string
	werr        error
	stalled     bool
	lastSection string
	sections    map[string]json.RawMessage
	sectionErrs []string
}

func (r psRun) errText() string {
	if r.scriptErr != "" {
		return r.scriptErr
	}
	if r.werr != nil {
		return r.werr.Error()
	}
	return "no result"
}

func (r psRun) finish(ctx context.Context, timeout time.Duration) (json.RawMessage, error) {
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("stopped after %v (time limit)", timeout)
	}
	if r.scriptErr != "" {
		return nil, errors.New(r.scriptErr)
	}
	if r.werr != nil && r.result == nil {
		return nil, fmt.Errorf("script failed: %v", r.werr)
	}
	if r.result == nil {
		return nil, errors.New("script finished without a result")
	}
	if !json.Valid(r.result) {
		return nil, errors.New("script returned invalid JSON")
	}
	return r.result, nil
}

// runPS runs one PowerShell script. stall > 0: stop it if it prints nothing for that long.
func (a *Agent) runPS(ctx context.Context, path string, env map[string]string, stall time.Duration, onLine func(string)) psRun {
	out := psRun{sections: map[string]json.RawMessage{}}
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(rctx, powershellPath(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	hideWindow(cmd)
	// Stopping must end the whole tree (sfc, DISM, MpCmdRun… started by the script), not just PowerShell.
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = 10 * time.Second
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		out.werr = err
		return out
	}
	// If it's stopped but something it started still holds the output open, stop reading anyway.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-rctx.Done():
			select {
			case <-time.After(12 * time.Second):
				stdout.Close()
			case <-finished:
			}
		case <-finished:
		}
	}()
	var mu sync.Mutex
	last := time.Now()
	if stall > 0 {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-rctx.Done():
					return
				case <-t.C:
					mu.Lock()
					idle := time.Since(last)
					mu.Unlock()
					if idle > stall {
						mu.Lock()
						out.stalled = true
						mu.Unlock()
						stop()
						return
					}
				}
			}
		}()
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		mu.Lock()
		last = time.Now()
		mu.Unlock()
		switch {
		case strings.HasPrefix(line, "##RESULT##"):
			out.result = json.RawMessage(strings.TrimPrefix(line, "##RESULT##"))
		case strings.HasPrefix(line, "##ERROR##"):
			out.scriptErr = strings.TrimPrefix(line, "##ERROR##")
		case strings.HasPrefix(line, "##SECTION##"):
			var sct struct {
				N string          `json:"n"`
				V json.RawMessage `json:"v"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "##SECTION##")), &sct) == nil && sct.N != "" {
				if sct.N == "_error" {
					var m string
					json.Unmarshal(sct.V, &m)
					out.sectionErrs = append(out.sectionErrs, m)
				} else {
					out.sections[sct.N] = sct.V
				}
			}
		case strings.TrimSpace(line) != "":
			if strings.HasPrefix(line, "Scanning: ") {
				out.lastSection = strings.TrimSpace(strings.TrimPrefix(line, "Scanning: "))
			}
			onLine(line)
		}
	}
	out.werr = cmd.Wait()
	mu.Lock()
	defer mu.Unlock()
	return out
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
	// Customer screen on the PC itself: read-only, local connections only, own view key.
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(iconPNG)
	})
	mux.HandleFunc("GET /screen", func(w http.ResponseWriter, r *http.Request) {
		if !isLocal(r) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(screenHTML)
	})
	mux.HandleFunc("GET /screen/state", func(w http.ResponseWriter, r *http.Request) {
		if !isLocal(r) || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("v")), []byte(a.viewToken)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		host := hostName()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.screenSeen, a.screenClosed = time.Now(), time.Time{}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		jobs := a.jobs
		if jobs == nil {
			jobs = []*Job{}
		}
		writeJSON(w, map[string]any{"computer": host, "version": Version, "phone": a.phoneSeen, "resumed": a.resumed, "remote": a.remoteStatus(),
			"url": a.controlURL, "qr": a.qrSVG, "jobs": jobs, "startedAt": a.started})
	})
	// The screen window is being closed: end the session (unless it reloads, or a restart we started is underway).
	mux.HandleFunc("POST /screen/closed", func(w http.ResponseWriter, r *http.Request) {
		if !isLocal(r) || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("v")), []byte(a.viewToken)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		a.mu.Lock()
		armed := a.resumeArmed
		a.screenClosed = time.Now()
		a.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		if armed {
			return
		}
		go func() {
			time.Sleep(5 * time.Second)
			a.mu.Lock()
			still := !a.screenClosed.IsZero() // no new poll since: really closed
			a.mu.Unlock()
			if still {
				a.emit(Event{Kind: "line", Job: &Job{}, Text: "Customer screen closed — ending the session."})
				a.Finish()
			}
		}()
	})
	mux.HandleFunc("GET /api/info", a.authed(func(w http.ResponseWriter, r *http.Request) {
		host := hostName()
		a.mu.Lock()
		defer a.mu.Unlock()
		writeJSON(w, map[string]any{"agent": "Turbo Button", "version": Version, "computer": host, "catalog": activeCatalog(), "os": agentOS(), "build": Build, "standby": a.relayConf.Standby, "relay": a.relayConf.URL, "relayStatus": a.remoteStatus(),
			"machineId": a.machineID, "resumed": a.resumed,
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
			if fmt.Sprint(j.ID) != id {
				continue
			}
			switch {
			case j.Status == "queued":
				j.Status = "cancelled"
				writeJSON(w, j)
				return
			case j.Status == "running" && j.stop != nil && (j.stoppable() || r.URL.Query().Get("force") == "1"):
				j.stopped, j.Forced = true, !j.stoppable()
				j.stop()
				writeJSON(w, j)
				return
			case j.Status == "running":
				writeErr(w, 409, "this one changes the system, so stopping it halfway could leave things half-done — force-stop it only if it's stuck")
				return
			}
		}
		writeErr(w, 400, "that job has already finished")
	}))
	// Cancel everything waiting, and stop running checks. A running job that changes the system
	// is left to finish (force-stop it on its own if it's stuck).
	mux.HandleFunc("POST /api/jobs/cancel-all", a.authed(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		cancelled, stopped, left := 0, 0, []string{}
		for _, j := range a.jobs {
			switch {
			case j.Status == "queued":
				j.Status, j.Held = "cancelled", false
				cancelled++
			case j.Status == "running" && j.stop != nil && j.stoppable():
				j.stopped = true
				j.stop()
				stopped++
			case j.Status == "running":
				left = append(left, j.Title)
			}
		}
		writeJSON(w, map[string]any{"cancelled": cancelled, "stopped": stopped, "stillRunning": left})
	}))
	mux.HandleFunc("POST /api/queue/pause", a.authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Paused bool `json:"paused"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
			writeErr(w, 400, "bad request")
			return
		}
		a.mu.Lock()
		a.paused = in.Paused
		for _, j := range a.jobs {
			if j.Status == "queued" {
				j.Held = in.Paused
			}
		}
		a.mu.Unlock()
		writeJSON(w, map[string]any{"paused": in.Paused})
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

// hostName is the PC's name; the simulated agent uses TURBO_MOCK_NAME for demos.
func hostName() string {
	if n := os.Getenv("TURBO_MOCK_NAME"); n != "" && mockMode() {
		return n
	}
	h, _ := os.Hostname()
	return h
}

func (a *Agent) relayStatus() string {
	if a.relay == nil {
		return ""
	}
	return a.relay.Status()
}

// remoteStatus is the one-line relay status shown on the customer screen (and to the phone).
func (a *Agent) remoteStatus() string {
	if a.relay != nil {
		return a.relay.Status()
	}
	if c := confFile(); c != "" {
		return "remote access off: " + c + " has no relay= and relaykey= lines"
	}
	dir := "the program's folder"
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	return "remote access off: no turbobutton.conf in " + dir
}

// scanStall: a scan section that prints nothing for this long is skipped.
var scanStall = 3 * time.Minute
