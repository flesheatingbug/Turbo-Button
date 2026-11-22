//go:build !windows

package main

// Development stubs so the agent can be exercised on Linux with TURBO_MOCK=1.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func setupConsole()          {}
func isAdmin() bool          { return true }
func relaunchAsAdmin() error { return nil }
func powershellPath() string { return "pwsh" }
func hideWindow(*exec.Cmd)   {}
func addFirewallRule(int)    {}
func removeFirewallRule()    {}
func mockMode() bool         { return os.Getenv("TURBO_MOCK") == "1" }

func mockRun(name string, env map[string]string, onLine func(string)) (json.RawMessage, error) {
	onLine("mock: running " + name)
	time.Sleep(300 * time.Millisecond)
	if name == "scan" {
		b, err := os.ReadFile("testdata/mock_scan.json")
		if err != nil {
			return json.RawMessage(`{"system":{"computerName":"MOCK-PC"}}`), nil
		}
		return b, nil
	}
	if name == "service_mode" && env["PCM_name"] == "FailMe" {
		return nil, fmt.Errorf("mock failure")
	}
	out, _ := json.Marshal(map[string]any{"mock": true, "action": name, "env": env, "freedBytes": 123456789})
	return out, nil
}
