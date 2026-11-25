//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestScanSkipsHungSection(t *testing.T) {
	if _, err := os.Stat("/tmp/fakeps/pwsh"); err != nil {
		t.Skip("no fake PowerShell")
	}
	t.Setenv("PATH", "/tmp/fakeps:"+os.Getenv("PATH"))
	saved := nativeRun
	nativeRun, scanStall = nil, 3*time.Second
	defer func() { nativeRun, scanStall = saved, 3*time.Minute }()
	a := &Agent{ctx: context.Background(), workDir: t.TempDir()}
	var lines []string
	res, err := a.runScript(context.Background(), "scan", nil, time.Minute, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	json.Unmarshal(res, &m)
	if m["system"] == nil || m["browsersAndNetwork"] == nil || !strings.Contains(string(m["errors"]), "performance") {
		t.Fatalf("unexpected result: %s", res)
	}
	t.Logf("result: %s", res)
	t.Logf("log: %s", strings.Join(lines, " | "))
}
