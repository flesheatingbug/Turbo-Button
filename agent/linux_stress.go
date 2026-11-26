//go:build linux

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cpuTempC reads the hottest CPU-ish sensor from /sys/class/hwmon (millidegrees C).
func cpuTempC() (float64, bool) {
	best, ok := 0.0, false
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range hwmons {
		name := strings.TrimSpace(readStr(h + "/name"))
		inputs, _ := filepath.Glob(h + "/temp*_input")
		for _, in := range inputs {
			b, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			mv, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				continue
			}
			c := float64(mv) / 1000.0
			if c <= 0 || c > 125 {
				continue
			}
			label := strings.TrimSpace(readStr(strings.Replace(in, "_input", "_label", 1)))
			// Prefer CPU package sensors; otherwise keep the hottest plausible reading.
			isCPU := strings.Contains(name, "coretemp") || strings.Contains(name, "k10temp") || strings.Contains(name, "zenpower") || strings.Contains(strings.ToLower(label), "package") || strings.Contains(strings.ToLower(label), "tctl")
			if c > best || (isCPU && !ok) {
				best, ok = c, true
			}
		}
	}
	return best, ok
}

// actStressTest loads the CPU hard for the requested time, watching temperature, and stops early
// if it reaches 97 C. Read-only. The goroutines stop when ctx is cancelled (Stop button) or time's up.
func actStressTest(ctx context.Context, env map[string]string, onLine func(string)) (map[string]any, error) {
	mins, _ := strconv.Atoi(env["PCM_minutes"])
	if mins < 1 {
		mins = 1
	}
	target := env["PCM_target"]
	if target == "" {
		target = "cpu"
	}
	cores := runtime.NumCPU()
	dur := time.Duration(mins) * time.Minute
	deadline := time.Now().Add(dur)
	idle, haveTemp := cpuTempC()
	if haveTemp {
		onLine("Stress test: " + target + " for " + strconv.Itoa(mins) + " min on " + strconv.Itoa(cores) + " core(s). Idle temperature " + trimF(idle) + " C.")
	} else {
		onLine("Stress test: " + target + " for " + strconv.Itoa(mins) + " min on " + strconv.Itoa(cores) + " core(s). (No temperature sensor found; watching for crashes only.)")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < cores; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0.0001
			for {
				select {
				case <-stop:
					return
				default:
				}
				for k := 0; k < 2_000_000; k++ {
					x = math.Sqrt(x*1.0000001 + 1.2345)
					if x > 1e9 {
						x = 0.0001
					}
				}
			}
		}()
	}
	var memHold [][]byte
	if target == "memory" || target == "all" {
		useMB := 1024
		if mt := readStr("/proc/meminfo"); mt != "" {
			for _, l := range strings.Split(mt, "\n") {
				if strings.HasPrefix(l, "MemAvailable:") {
					if kb, err := strconv.Atoi(strings.Fields(l)[1]); err == nil {
						useMB = clampInt(kb/1024*6/10, 512, 8192)
					}
				}
			}
		}
		onLine("Allocating about " + strconv.Itoa(useMB) + " MB of RAM to exercise.")
		for m := 0; m < useMB; m += 256 {
			memHold = append(memHold, make([]byte, 256<<20))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, a := range memHold {
					for i := 0; i < len(a); i += 4096 {
						a[i]++
					}
				}
			}
		}()
	}

	maxTemp, stoppedEarly, reason := idle, false, ""
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			reason = "stopped by the technician"
			break loop
		case <-tick.C:
			if time.Now().After(deadline) {
				break loop
			}
			left := int(time.Until(deadline).Seconds())
			t, ok := cpuTempC()
			line := strconv.Itoa(left) + "s left"
			if ok {
				if t > maxTemp {
					maxTemp = t
				}
				line += " - temp " + trimF(t) + " C"
			}
			onLine(line)
			if ok && t >= 97 {
				stoppedEarly, reason = true, "CPU reached "+trimF(t)+" C"
				onLine("CPU reached " + trimF(t) + " C - stopping early to protect the PC.")
				break loop
			}
		}
	}
	close(stop)
	wg.Wait()
	memHold = nil
	runtime.GC()

	verdict := "stable"
	switch {
	case stoppedEarly:
		verdict = "overheated"
	case haveTemp && maxTemp >= 90:
		verdict = "hot but held"
	}
	note := map[string]string{
		"overheated":   "Stopped early: " + reason + ". Clean the fans and vents and check the thermal paste.",
		"hot but held": "Ran hot (max " + trimF(maxTemp) + " C) but stayed up. Worth cleaning fans/vents.",
		"stable":       "Held up for the full run" + ternary(haveTemp, " (max "+trimF(maxTemp)+" C).", " without crashing."),
	}[verdict]
	onLine(note)
	res := map[string]any{"target": target, "requestedSec": int(dur.Seconds()), "cores": cores, "tempAvailable": haveTemp,
		"stoppedEarly": stoppedEarly, "verdict": verdict, "note": note}
	if haveTemp {
		res["idleTempC"] = round1(idle)
		res["maxTempC"] = round1(maxTemp)
	}
	return res, nil
}

func trimF(f float64) string   { return strconv.FormatFloat(round1(f), 'f', -1, 64) }
func round1(f float64) float64 { return math.Round(f*10) / 10 }
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func ternary(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}
