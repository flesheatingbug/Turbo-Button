package main

// Resume-after-restart: when Turbo Button itself restarts the PC, it saves the session
// (same key, port, scans and work log) and schedules a one-time relaunch at the next
// logon. The phone just keeps retrying and reconnects to the same session.
// Everything is removed again when the agent comes back or the session ends.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type resumeState struct {
	Token        string          `json:"token"`
	Port         int             `json:"port"`
	AutoRestore  bool            `json:"autoRestore"`
	RestoreTried bool            `json:"restoreTried"`
	NextID       int             `json:"nextId"`
	Jobs         []*Job          `json:"jobs"`
	FirstScan    json.RawMessage `json:"firstScan"`
	LastScan     json.RawMessage `json:"lastScan"`
	Started      time.Time       `json:"started"`
	SavedAt      time.Time       `json:"savedAt"`
	Relay        relayConf       `json:"relay,omitempty"`
}

func resumeFile() string   { return filepath.Join(resumeDir(), "resume.json") }
func agentCopyDir() string { return filepath.Join(resumeDir(), "agent") }

// prepareResume is called right before an action that restarts the PC.
func (a *Agent) prepareResume(port int) error {
	if err := os.MkdirAll(agentCopyDir(), 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	target := filepath.Join(agentCopyDir(), "TurboButton.exe")
	if filepath.Clean(exe) != filepath.Clean(target) {
		if err := copyFile(exe, target); err != nil {
			return fmt.Errorf("copy agent: %w", err)
		}
	}
	// The copy that restarts after the reboot needs the relay settings too (a conf file next to
	// the original isn't next to the copy; settings built into the exe travel with it).
	if c := confFile(); c != "" {
		copyFile(c, filepath.Join(agentCopyDir(), "turbobutton.conf"))
	}
	if err := a.writeResumeState(port); err != nil {
		return err
	}
	if err := scheduleResume(target); err != nil {
		return err
	}
	a.mu.Lock()
	a.resumeArmed = true
	a.mu.Unlock()
	return nil
}

// writeResumeState saves the session; called when arming and again after each job while a restart is pending.
func (a *Agent) writeResumeState(port int) error {
	a.mu.Lock()
	st := resumeState{Token: a.token, Port: port, AutoRestore: a.autoRestore, RestoreTried: a.restoreTried, NextID: a.nextID,
		Jobs: a.jobs, FirstScan: a.firstScan, LastScan: a.lastScan, Started: a.started, SavedAt: time.Now(), Relay: a.relayConf}
	b, err := json.Marshal(st)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.WriteFile(resumeFile(), b, 0600); err != nil {
		return err
	}
	lockDown(resumeFile())
	return nil
}

// cancelResume undoes prepareResume (restart cancelled, or session ended).
func cancelResume() {
	unscheduleResume()
	os.Remove(resumeFile())
}

// loadResume reads and immediately deletes the saved session.
func loadResume() (*resumeState, error) {
	b, err := os.ReadFile(resumeFile())
	unscheduleResume()
	os.Remove(resumeFile())
	if err != nil {
		return nil, err
	}
	var st resumeState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if time.Since(st.SavedAt) > 7*24*time.Hour {
		return nil, fmt.Errorf("saved session is too old")
	}
	return &st, nil
}

// restoreFrom puts a saved session back into a fresh agent.
func (a *Agent) restoreFrom(st *resumeState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token, a.autoRestore, a.restoreTried, a.nextID = st.Token, st.AutoRestore, st.RestoreTried, st.NextID
	a.firstScan, a.lastScan, a.started, a.resumed = st.FirstScan, st.LastScan, st.Started, true
	now := time.Now()
	for _, j := range st.Jobs {
		restarter := j.Action == "restart_pc" || j.Action == "defender_offline_scan"
		switch {
		case restarter && (j.Status == "running" || j.Status == "queued"):
			j.Status, j.Result, j.Error = "done", json.RawMessage(`{"note":"PC restarted"}`), ""
			j.Finished = &now
		case j.Status == "running":
			j.Status, j.Error = "failed", "interrupted by the restart"
			j.Finished = &now
		case j.Status == "queued":
			j.Status, j.Error = "cancelled", "not run: the PC restarted first"
		}
	}
	a.jobs = st.Jobs
}

// removeAgentCopy deletes the copy made for the restart (after we exit if we're running from it).
func removeAgentCopy() {
	exe, _ := os.Executable()
	if filepath.Dir(filepath.Clean(exe)) == filepath.Clean(agentCopyDir()) {
		deleteDirAfterExit(agentCopyDir())
		return
	}
	os.RemoveAll(agentCopyDir())
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0700)
}
