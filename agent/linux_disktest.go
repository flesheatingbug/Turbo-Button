//go:build linux

package main

// Drive surface test: reads the raw disk (read-only, never writes) to find unreadable sectors and
// regions that have gone slow — the signs of a failing HDD or SSD that SMART can miss. "sample"
// spans the whole disk quickly; "full" reads every sector (slow). Stop any time.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func diskList(ctx context.Context, only string) []map[string]string {
	var out1 []map[string]string
	for _, l := range strings.Split(out(context.Background(), 15*time.Second, "lsblk", "-dnro", "NAME,TYPE,SIZE,MODEL,ROTA"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[1] != "disk" {
			continue
		}
		dev := "/dev/" + f[0]
		if only != "" && only != dev {
			continue
		}
		kind := "SSD"
		if len(f) >= 5 && f[len(f)-1] == "1" {
			kind = "HDD"
		}
		model := ""
		if len(f) > 4 {
			model = strings.Join(f[3:len(f)-1], " ")
		}
		out1 = append(out1, map[string]string{"device": dev, "size": f[2], "model": model, "kind": kind})
	}
	return out1
}

// scanDisk reads the device and returns a health summary. sample=true reads ~300 spots across the
// whole disk; otherwise it reads the whole thing. ctx cancels it.
func scanDisk(ctx context.Context, dev string, sample bool, onLine func(string)) map[string]any {
	res := map[string]any{"device": dev}
	f, err := os.OpenFile(dev, os.O_RDONLY, 0)
	if err != nil {
		res["error"] = err.Error()
		onLine("Can't open " + dev + ": " + err.Error())
		return res
	}
	defer f.Close()
	size, _ := f.Seek(0, io.SeekEnd)
	if size <= 0 {
		res["error"] = "couldn't read device size"
		return res
	}
	const chunk = 4 << 20
	var step int64 = chunk
	spots := size / chunk
	if sample && spots > 300 {
		step = size / 300
		step -= step % 512
		if step < chunk {
			step = chunk
		}
		spots = 300
	}
	buf := make([]byte, chunk)
	var readBytes int64
	var badOffsets []string
	var errCount, slowCount int
	minMBps, maxMBps := 1e18, 0.0
	t0 := time.Now()
	lastLog := time.Now()
	for off := int64(0); off < size; off += step {
		select {
		case <-ctx.Done():
			onLine("Stopped.")
			res["stopped"] = true
			goto done
		default:
		}
		n := chunk
		if off+int64(n) > size {
			n = int(size - off)
		}
		ts := time.Now()
		got, rerr := f.ReadAt(buf[:n], off)
		dt := time.Since(ts).Seconds()
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			errCount++
			if len(badOffsets) < 50 {
				badOffsets = append(badOffsets, fmt.Sprintf("%.1f GB", float64(off)/1e9))
			}
			onLine(fmt.Sprintf("Read error at %.1f GB on %s", float64(off)/1e9, dev))
			continue
		}
		readBytes += int64(got)
		if dt > 0 {
			mbps := float64(got) / 1e6 / dt
			if mbps < minMBps {
				minMBps = mbps
			}
			if mbps > maxMBps {
				maxMBps = mbps
			}
			if mbps < 10 {
				slowCount++
			}
		}
		if time.Since(lastLog) > 5*time.Second {
			lastLog = time.Now()
			pct := float64(off) / float64(size) * 100
			onLine(fmt.Sprintf("%s: %.0f%% scanned, %d error(s), slowest %.0f MB/s", dev, pct, errCount, minMBps))
		}
	}
done:
	elapsed := time.Since(t0).Seconds()
	verdict := "healthy"
	switch {
	case errCount > 0:
		verdict = "FAILING (read errors)"
	case slowCount > 3:
		verdict = "suspect (slow regions)"
	}
	avg := 0.0
	if elapsed > 0 {
		avg = float64(readBytes) / 1e6 / elapsed
	}
	res["scannedGB"] = round1(float64(readBytes) / 1e9)
	res["readErrors"] = errCount
	res["errorOffsets"] = badOffsets
	res["slowRegions"] = slowCount
	if minMBps < 1e17 {
		res["minMBps"] = round1(minMBps)
		res["avgMBps"] = round1(avg)
	}
	res["verdict"] = verdict
	res["mode"] = map[bool]string{true: "sample", false: "full"}[sample]
	onLine(fmt.Sprintf("%s: %s — %d error(s), %d slow region(s), avg %.0f MB/s.", dev, verdict, errCount, slowCount, avg))
	return res
}

func actDiskTest(ctx context.Context, env map[string]string, onLine func(string)) (map[string]any, error) {
	sample := env["PCM_mode"] != "full"
	disks := diskList(ctx, env["PCM_device"])
	if len(disks) == 0 {
		return nil, fmt.Errorf("no drives found to test")
	}
	var results []map[string]any
	for _, d := range disks {
		onLine(fmt.Sprintf("Testing %s (%s %s %s), %s...", d["device"], d["kind"], d["size"], d["model"], map[bool]string{true: "quick surface scan", false: "full read test"}[sample]))
		r := scanDisk(ctx, d["device"], sample, onLine)
		for k, v := range d {
			if _, ok := r[k]; !ok {
				r[k] = v
			}
		}
		results = append(results, r)
	}
	return map[string]any{"drives": results, "mode": map[bool]string{true: "sample", false: "full"}[sample]}, nil
}
