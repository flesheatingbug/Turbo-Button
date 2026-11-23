//go:build !windows

package main

// Fake results so the agent and the phone UI can be exercised without a real PC (TURBO_MOCK=1).

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

var mockScans int

func mockMode() bool { return os.Getenv("TURBO_MOCK") == "1" }

func mockRun(name string, env map[string]string, onLine func(string)) (json.RawMessage, error) {
	onLine("Starting " + name + "...")
	time.Sleep(300 * time.Millisecond)
	// TURBO_MOCK_DELAY=ms makes each simulated job take a while (for demos and screen recordings).
	if d, _ := time.ParseDuration(os.Getenv("TURBO_MOCK_DELAY") + "ms"); d > 0 && name != "scan" {
		for i := 1; i <= 4; i++ {
			time.Sleep(d / 4)
			onLine(fmt.Sprintf("Working... step %d of 4", i))
		}
	}
	if name == "scan" {
		mockScans++
		b, err := os.ReadFile("testdata/mock_scan.json")
		if err != nil {
			return json.RawMessage(`{"system":{"computerName":"MOCK-PC"}}`), nil
		}
		if mockScans > 1 { // pretend the fixes worked
			var m map[string]any
			json.Unmarshal(b, &m)
			m["scannedAt"] = time.Now().Format("2006-01-02T15:04:05")
			if v, ok := m["volumes"].([]any); ok && len(v) > 0 {
				v[0].(map[string]any)["freeGB"] = 31.4
				v[0].(map[string]any)["freePct"] = 13.2
			}
			m["cleanupPotentialGB"] = map[string]any{"user_temp": 0.1, "windows_temp": 0, "update_cache": 0, "browser_cache": 0.2, "recycle_bin": 5.5}
			if st, ok := m["startup"].([]any); ok && len(st) > 0 {
				st[0].(map[string]any)["enabled"] = false
			}
			b, _ = json.Marshal(m)
		}
		return b, nil
	}
	canned := map[string]string{
		"sfc_verify":    `{"verdict":"corruption found","corrupt":true,"exitCode":1}`,
		"dism_check":    `{"verdict":"corruption found (repairable)","corrupt":true,"exitCode":0}`,
		"app_updates":   `{"mode":"list","available":4,"output":"4 upgrades available."}`,
		"chkdsk_scan":   `{"drive":"C","exitCode":0,"problemsFound":false,"output":"no problems"}`,
		"defender_scan": `{"scanType":"QuickScan","minutes":3.1,"detections":[{"name":"Adware:Win32/OpenCandy","resources":"file:C:\\Users\\anne\\Downloads\\setup.exe"}]}`,
		"disk_usage":    `{"folders":[{"path":"C:\\Users\\anne\\Downloads","gb":48.2},{"path":"C:\\Windows","gb":27.9}],"note":"Largest: Downloads 48.2 GB"}`,
	}
	if c, ok := canned[name]; ok && !(name == "app_updates" && env["PCM_mode"] == "install") {
		return json.RawMessage(c), nil
	}
	if name == "windows_update" && env["PCM_mode"] == "check" {
		return json.RawMessage(`{"mode":"check","found":2,"updates":[{"title":"2026-11 Cumulative Update (KB5099999)","driver":false},{"title":"Intel - Display - 31.0.101","driver":true}]}`), nil
	}
	if name == "service_mode" && env["PCM_name"] == "FailMe" {
		return nil, fmt.Errorf("mock failure")
	}
	if name == "windows_update" {
		for _, l := range []string{"Searching Windows Update (this can take a few minutes)...", "Found: 2026-11 Cumulative Update for Windows 11 (KB5099999)", "[1/1] Downloading: 2026-11 Cumulative Update", "[1/1] Installing: 2026-11 Cumulative Update", "   installed"} {
			onLine(l)
			time.Sleep(150 * time.Millisecond)
		}
		return json.RawMessage(`{"mode":"install","found":1,"installed":1,"failed":0,"rebootRequired":true}`), nil
	}
	res := map[string]any{"mock": true, "action": name}
	if name == "cleanup" || name == "component_cleanup" {
		res["freedBytes"] = 400000000 + int64(len(env["PCM_target"]))*97000000
	}
	out, _ := json.Marshal(res)
	return out, nil
}
