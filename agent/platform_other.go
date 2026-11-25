//go:build !windows && !linux

package main

// Development stubs so the agent can be exercised on Linux with TURBO_MOCK=1.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func setupConsole() bool     { return true }
func keepAwake() func()      { return func() {} }
func isAdmin() bool          { return true }
func relaunchAsAdmin() error { return nil }
func powershellPath() string { return "pwsh" }
func hideWindow(*exec.Cmd)   {}
func addFirewallRule(int)    {}
func removeFirewallRule()    {}

func resumeDir() string {
	if d := os.Getenv("TURBO_RESUME_DIR"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "turbobutton-resume")
}
func lockDown(string)             {}
func scheduleResume(string) error { return nil }
func unscheduleResume()           {}
func deleteDirAfterExit(string)   {}
func machineID() string {
	if id := os.Getenv("TURBO_MOCK_ID"); id != "" {
		return id
	}
	return "mock-machine-1"
}
func openScreen(url, workDir string) error { return fmt.Errorf("customer screen is Windows-only") }
func closeScreen(string)                   {}
func hideConsole() bool                    { return false }
func desktopAlert(msg string)              {}
func showConsole()                         {}

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
