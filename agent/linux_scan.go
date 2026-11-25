//go:build linux

package main

// Linux scan: reads /proc, /sys and standard tools. Every section is independent, so a
// missing tool (smartctl, lspci, flatpak…) just leaves that part out.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func init() { nativeRun = linuxRun }

// ---------- helpers ----------

func out(ctx context.Context, timeout time.Duration, name string, args ...string) string {
	if !have(name) {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	b, _ := cmd.Output()
	return string(b)
}

func readStr(p string) string { b, _ := os.ReadFile(p); return strings.TrimSpace(string(b)) }

func gb(b int64) float64 { return math.Round(float64(b)/1e9*100) / 100 } // decimal GB, like disk vendors and df -H

func dirSize(root string) int64 {
	var sum int64
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				sum += fi.Size()
			}
		}
		return nil
	})
	return sum
}

func globSize(patterns ...string) int64 {
	var sum int64
	for _, pat := range patterns {
		m, _ := filepath.Glob(pat)
		for _, p := range m {
			sum += dirSize(p)
		}
	}
	return sum
}

type luser struct {
	name, home string
	uid        int
}

// humans returns real login users (uid 1000-59999 with an existing home).
func humans() []luser {
	var us []luser
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return us
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(p[2])
		if uid < 1000 || uid >= 60000 || strings.HasSuffix(p[6], "nologin") || strings.HasSuffix(p[6], "false") {
			continue
		}
		if fi, err := os.Stat(p[5]); err == nil && fi.IsDir() {
			us = append(us, luser{p[0], p[5], uid})
		}
	}
	return us
}

func pkgManager() string {
	for _, pm := range []string{"pacman", "apt-get", "dnf", "zypper"} {
		if have(pm) {
			return pm
		}
	}
	return ""
}

func pkgCacheDirs() []string {
	switch pkgManager() {
	case "pacman":
		return []string{"/var/cache/pacman/pkg"}
	case "apt-get":
		return []string{"/var/cache/apt/archives"}
	case "dnf":
		return []string{"/var/cache/dnf", "/var/cache/libdnf5"}
	case "zypper":
		return []string{"/var/cache/zypp/packages"}
	}
	return nil
}

func meminfo() map[string]int64 {
	m := map[string]int64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.Fields(v)[0], 10, 64)
		m[k] = n * 1024
	}
	return m
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "..."
	}
	return s
}

// ---------- scan ----------

func linuxScan(ctx context.Context, onLine func(string)) map[string]any {
	r := map[string]any{"scannedAt": time.Now().Format("2006-01-02T15:04:05"), "errors": []string{}}
	section := func(name string, f func() any) {
		if ctx.Err() != nil {
			return
		}
		onLine("Scanning: " + name)
		defer func() {
			if e := recover(); e != nil {
				r["errors"] = append(r["errors"].([]string), fmt.Sprintf("%s: %v", name, e))
			}
		}()
		if v := f(); v != nil {
			r[name] = v
		}
	}
	section("system", func() any { return scanSystem(ctx) })
	section("gpu", func() any { return scanGPU(ctx) })
	section("volumes", func() any { return scanVolumes(ctx) })
	section("physicalDisks", func() any { return scanDisks(ctx) })
	section("cleanupPotentialGB", func() any { return scanCleanup(ctx) })
	section("startup", func() any { return scanAutostart() })
	section("services", func() any { return scanServices(ctx) })
	section("apps", func() any { return scanFlatpaks(ctx) })
	section("programs", func() any { return scanPackages(ctx) })
	section("updates", func() any { return scanUpdates(ctx) })
	section("events", func() any { return scanJournal(ctx) })
	section("bootPerformance", func() any { return scanBoot(ctx) })
	section("performance", func() any { return scanPerf() })
	section("battery", func() any { return scanBattery() })
	section("security", func() any { return scanSecurity(ctx) })
	section("browsersAndNetwork", func() any { return scanBrowsers() })
	section("restorePoints", func() any { return scanSnapshots(ctx) })
	return r
}

func scanSystem(ctx context.Context) any {
	osr := readKV("/etc/os-release")
	host, _ := os.Hostname()
	up, _ := strconv.ParseFloat(strings.Fields(readStr("/proc/uptime") + " 0")[0], 64)
	cpu := ""
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
				cpu = strings.TrimSpace(v)
				break
			}
		}
		f.Close()
	}
	mi := meminfo()
	bats, _ := filepath.Glob("/sys/class/power_supply/BAT*")
	g := desktopUser()
	signed := ""
	if g != nil {
		signed = g.name
	}
	return map[string]any{
		"computerName": host, "osFamily": "linux", "os": osr["PRETTY_NAME"], "displayVersion": osr["VERSION_ID"], "edition": osr["ID"],
		"idLike": osr["ID_LIKE"], "build": readStr("/proc/sys/kernel/osrelease"), "packageManager": pkgManager(),
		"lastBoot": time.Now().Add(-time.Duration(up) * time.Second).Format("2006-01-02T15:04:05"), "uptimeHours": math.Round(up/360) / 10,
		"manufacturer": readStr("/sys/class/dmi/id/sys_vendor"), "model": readStr("/sys/class/dmi/id/product_name"),
		"bios": strings.TrimSpace(readStr("/sys/class/dmi/id/bios_version") + " (" + readStr("/sys/class/dmi/id/bios_date") + ")"),
		"cpu":  cpu, "threads": runtime.NumCPU(), "cores": runtime.NumCPU(),
		"ramGB": gb(mi["MemTotal"]), "freeRamGB": gb(mi["MemAvailable"]), "isLaptop": len(bats) > 0,
		"signedInUser": signed, "agentRunningAs": "root", "partOfDomain": have("realm") && strings.Contains(out(ctx, 5*time.Second, "realm", "list"), "configured"),
		"desktop": desktopName(),
	}
}

func desktopName() string {
	if g := desktopUser(); g != nil {
		for _, e := range g.env {
			if strings.HasPrefix(e, "XDG_SESSION_TYPE=") {
				return strings.TrimPrefix(e, "XDG_SESSION_TYPE=")
			}
		}
	}
	return ""
}

func scanGPU(ctx context.Context) any {
	var gs []map[string]any
	var cur map[string]any
	for _, l := range strings.Split(out(ctx, 10*time.Second, "lspci", "-k"), "\n") {
		if !strings.HasPrefix(l, "\t") && (strings.Contains(l, "VGA") || strings.Contains(l, "3D controller") || strings.Contains(l, "Display controller")) {
			_, name, _ := strings.Cut(l, ": ")
			cur = map[string]any{"name": name}
			gs = append(gs, cur)
		} else if cur != nil && strings.Contains(l, "Kernel driver in use:") {
			cur["driver"] = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
		} else if !strings.HasPrefix(l, "\t") {
			cur = nil
		}
	}
	return gs
}

func scanVolumes(ctx context.Context) any {
	var vs []map[string]any
	lines := strings.Split(out(ctx, 10*time.Second, "df", "-B1", "--output=source,fstype,size,avail,target", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "overlay", "-x", "efivarfs"), "\n")
	seen := map[string]bool{}
	for _, l := range lines[min(1, len(lines)):] {
		f := strings.Fields(l)
		if len(f) < 5 || !strings.HasPrefix(f[0], "/dev/") || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		size, _ := strconv.ParseInt(f[2], 10, 64)
		avail, _ := strconv.ParseInt(f[3], 10, 64)
		if size == 0 {
			continue
		}
		vs = append(vs, map[string]any{"drive": f[4], "label": f[0], "fs": f[1], "sizeGB": gb(size), "freeGB": gb(avail),
			"freePct": math.Round(1000*float64(avail)/float64(size)) / 10, "health": "Healthy"})
	}
	sort.Slice(vs, func(i, j int) bool { return len(vs[i]["drive"].(string)) < len(vs[j]["drive"].(string)) })
	return vs
}

func scanDisks(ctx context.Context) any {
	var lb struct {
		Blockdevices []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
			Size  any    `json:"size"`
			Rota  any    `json:"rota"`
			Tran  string `json:"tran"`
			Type  string `json:"type"`
		} `json:"blockdevices"`
	}
	json.Unmarshal([]byte(out(ctx, 10*time.Second, "lsblk", "-J", "-b", "-d", "-o", "NAME,MODEL,SIZE,ROTA,TRAN,TYPE")), &lb)
	var ds []map[string]any
	for _, d := range lb.Blockdevices {
		if d.Type != "disk" || strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "loop") {
			continue
		}
		size, _ := strconv.ParseFloat(fmt.Sprint(d.Size), 64)
		rota := fmt.Sprint(d.Rota) == "true" || fmt.Sprint(d.Rota) == "1"
		media := "SSD"
		if rota {
			media = "HDD"
		}
		dev := "/dev/" + d.Name
		m := map[string]any{"name": strings.TrimSpace(d.Model), "device": dev, "mediaType": media, "bus": d.Tran, "sizeGB": gb(int64(size)), "health": "unknown (install smartmontools)"}
		if s := out(ctx, 20*time.Second, "smartctl", "-j", "-a", dev); s != "" {
			var sm map[string]any
			if json.Unmarshal([]byte(s), &sm) == nil {
				addSmart(m, sm)
			}
		}
		if m["name"] == "" {
			m["name"] = d.Name
		}
		ds = append(ds, m)
	}
	return ds
}

func num(v any) (float64, bool) { f, ok := v.(float64); return f, ok }
func obj(v any) map[string]any  { m, _ := v.(map[string]any); return m }

func addSmart(m, sm map[string]any) {
	if st := obj(sm["smart_status"]); st != nil {
		if p, ok := st["passed"].(bool); ok {
			m["health"] = map[bool]string{true: "Healthy", false: "FAILING"}[p]
		}
	}
	if t, ok := num(obj(sm["temperature"])["current"]); ok {
		m["tempC"] = t
	}
	if h, ok := num(obj(sm["power_on_time"])["hours"]); ok {
		m["powerOnHours"] = h
	}
	if n := obj(sm["nvme_smart_health_information_log"]); n != nil {
		if v, ok := num(n["percentage_used"]); ok {
			m["wearPct"] = v
		}
		if v, ok := num(n["media_errors"]); ok {
			m["mediaErrors"] = v
		}
		if v, ok := num(n["unsafe_shutdowns"]); ok {
			m["unsafeShutdowns"] = v
		}
	}
	if t := obj(sm["ata_smart_attributes"]); t != nil {
		tbl, _ := t["table"].([]any)
		for _, row := range tbl {
			a := obj(row)
			id, _ := num(a["id"])
			raw, _ := num(obj(a["raw"])["value"])
			switch int(id) {
			case 5:
				m["reallocatedSectors"] = raw
			case 187:
				m["reportedUncorrectable"] = raw
			case 197:
				m["pendingSectors"] = raw
			case 198:
				m["offlineUncorrectable"] = raw
			case 199:
				m["crcErrors"] = raw
			case 177, 231, 233:
				if v, ok := num(a["value"]); ok && m["wearPct"] == nil && (int(id) == 177 || int(id) == 231) {
					m["wearPct"] = 100 - v // normalised "life left" → wear
				}
			}
		}
	}
	if l := obj(sm["ata_smart_self_test_log"]); l != nil {
		if tab, _ := obj(l["standard"])["table"].([]any); len(tab) > 0 {
			m["lastSelfTest"] = obj(obj(tab[0])["status"])["string"]
		}
	}
}

func scanCleanup(ctx context.Context) any {
	us := humans()
	var cache, thumbs, trash []string
	for _, u := range us {
		cache = append(cache, filepath.Join(u.home, ".cache"))
		thumbs = append(thumbs, filepath.Join(u.home, ".cache/thumbnails"))
		trash = append(trash, filepath.Join(u.home, ".local/share/Trash"))
	}
	return map[string]any{
		"pkg_cache": gb(globSize(pkgCacheDirs()...)), "journal": gb(globSize("/var/log/journal")),
		"user_cache": gb(globSize(cache...)), "thumbnails": gb(globSize(thumbs...)), "trash": gb(globSize(trash...)),
		"coredumps": gb(globSize("/var/lib/systemd/coredump")), "tmp": gb(oldFilesSize("/var/tmp", 10*24*time.Hour)),
	}
}

func oldFilesSize(root string, age time.Duration) int64 {
	var sum int64
	cut := time.Now().Add(-age)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil && fi.ModTime().Before(cut) {
				sum += fi.Size()
			}
		}
		return nil
	})
	return sum
}

var reExecField = regexp.MustCompile(`(?m)^(Name|GenericName|Comment|Exec|Hidden|X-GNOME-Autostart-enabled|OnlyShowIn|NotShowIn)=(.*)$`)

func desktopEntry(p string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(p)
	if err != nil {
		return m
	}
	s := string(b)
	if i := strings.Index(s, "\n["); i > 0 { // only the [Desktop Entry] group
		s = s[:i]
	}
	for _, x := range reExecField.FindAllStringSubmatch(s, -1) {
		if _, dup := m[x[1]]; !dup {
			m[x[1]] = strings.TrimSpace(x[2])
		}
	}
	return m
}

func entryEnabled(e map[string]string) bool {
	return !strings.EqualFold(e["Hidden"], "true") && !strings.EqualFold(e["X-GNOME-Autostart-enabled"], "false")
}

func scanAutostart() any {
	var items []map[string]any
	sys, _ := filepath.Glob("/etc/xdg/autostart/*.desktop")
	us := humans()
	for _, p := range sys {
		e := desktopEntry(p)
		enabled := entryEnabled(e)
		// a per-user override with the same file name wins
		for _, u := range us {
			if o := desktopEntry(filepath.Join(u.home, ".config/autostart", filepath.Base(p))); len(o) > 0 && !entryEnabled(o) {
				enabled = false
			}
		}
		items = append(items, map[string]any{"location": "system autostart", "name": firstNonEmpty(e["Name"], filepath.Base(p)), "description": short(firstNonEmpty(e["Comment"], e["GenericName"]), 160), "onlyIn": e["OnlyShowIn"], "command": short(e["Exec"], 120), "path": p, "enabled": enabled})
	}
	for _, u := range us {
		ps, _ := filepath.Glob(filepath.Join(u.home, ".config/autostart/*.desktop"))
		for _, p := range ps {
			if _, err := os.Stat(filepath.Join("/etc/xdg/autostart", filepath.Base(p))); err == nil {
				continue // an override of a system entry, already shown
			}
			e := desktopEntry(p)
			items = append(items, map[string]any{"location": "autostart (" + u.name + ")", "name": firstNonEmpty(e["Name"], filepath.Base(p)), "description": short(firstNonEmpty(e["Comment"], e["GenericName"]), 160), "onlyIn": e["OnlyShowIn"], "command": short(e["Exec"], 120), "path": p, "enabled": entryEnabled(e)})
		}
	}
	return items
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func scanServices(ctx context.Context) any {
	var svcs []map[string]any
	for _, l := range strings.Split(out(ctx, 10*time.Second, "systemctl", "list-units", "--failed", "--plain", "--no-legend", "--no-pager"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 {
			svcs = append(svcs, map[string]any{"name": f[0], "state": "failed", "startMode": "enabled", "displayName": short(strings.Join(f[4:], " "), 80), "failed": true})
		}
	}
	for _, l := range strings.Split(out(ctx, 10*time.Second, "systemctl", "list-unit-files", "--type=service", "--state=enabled", "--plain", "--no-legend", "--no-pager"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 1 && strings.HasSuffix(f[0], ".service") {
			svcs = append(svcs, map[string]any{"name": f[0], "state": "enabled", "startMode": "Auto"})
		}
	}
	return svcs
}

func scanFlatpaks(ctx context.Context) any {
	var apps []map[string]any
	for _, l := range strings.Split(out(ctx, 15*time.Second, "flatpak", "list", "--app", "--columns=application,version,origin"), "\n") {
		f := strings.Split(l, "\t")
		if len(f) >= 1 && strings.TrimSpace(f[0]) != "" {
			a := map[string]any{"name": strings.TrimSpace(f[0]), "kind": "flatpak", "likelyBloat": false}
			if len(f) > 1 {
				a["version"] = strings.TrimSpace(f[1])
			}
			apps = append(apps, a)
		}
	}
	for _, l := range strings.Split(out(ctx, 15*time.Second, "snap", "list"), "\n")[1:] {
		f := strings.Fields(l)
		if len(f) >= 2 {
			apps = append(apps, map[string]any{"name": f[0], "version": f[1], "kind": "snap", "likelyBloat": false})
		}
	}
	return apps
}

func scanPackages(ctx context.Context) any {
	var lines []string
	switch pkgManager() {
	case "pacman":
		lines = strings.Split(out(ctx, 15*time.Second, "pacman", "-Qe"), "\n")
	case "apt-get":
		lines = strings.Split(out(ctx, 20*time.Second, "apt-mark", "showmanual"), "\n")
	case "dnf":
		lines = strings.Split(out(ctx, 30*time.Second, "dnf", "repoquery", "--userinstalled", "--qf", "%{name} %{version}", "-q"), "\n")
	case "zypper":
		lines = strings.Split(out(ctx, 20*time.Second, "rpm", "-qa", "--qf", "%{NAME} %{VERSION}\\n"), "\n")
	}
	var ps []map[string]any
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		p := map[string]any{"name": f[0], "uninstall": "package"}
		if len(f) > 1 {
			p["version"] = f[1]
		}
		ps = append(ps, p)
		if len(ps) >= 500 {
			break
		}
	}
	return ps
}

func scanUpdates(ctx context.Context) any {
	u := map[string]any{"packageManager": pkgManager(), "wingetAvailable": false, "flatpak": have("flatpak"), "fwupd": have("fwupdmgr")}
	pending := -1
	switch pkgManager() {
	case "pacman":
		if have("checkupdates") {
			pending = countLines(out(ctx, 60*time.Second, "checkupdates"))
		} else {
			pending = countLines(out(ctx, 15*time.Second, "pacman", "-Qu"))
		}
		u["lastUpdateInstalled"] = lastPacmanUpgrade()
	case "apt-get":
		if s := strings.TrimSpace(out(ctx, 30*time.Second, "sh", "-c", "apt list --upgradable 2>/dev/null | grep -c /")); s != "" {
			pending, _ = strconv.Atoi(s)
		}
		u["lastUpdateInstalled"] = fileTime("/var/log/apt/history.log")
	case "dnf":
		pending = countLines(out(ctx, 60*time.Second, "dnf", "-q", "-C", "check-update"))
		u["lastUpdateInstalled"] = fileTime("/var/log/dnf.rpm.log")
	case "zypper":
		pending = countLines(out(ctx, 60*time.Second, "sh", "-c", "zypper -q --no-refresh lu | grep -c '^v ' || true"))
		u["lastUpdateInstalled"] = fileTime("/var/log/zypp/history")
	}
	if pending >= 0 {
		u["pendingFromCache"] = pending
	}
	if t, ok := u["lastUpdateInstalled"].(string); ok && t != "" {
		if tt, err := time.Parse("2006-01-02T15:04:05", t); err == nil {
			u["daysSinceLastUpdate"] = int(time.Since(tt).Hours() / 24)
		}
	}
	running := readStr("/proc/sys/kernel/osrelease")
	_, modErr := os.Stat("/usr/lib/modules/" + running)
	_, modErr2 := os.Stat("/lib/modules/" + running)
	_, rr := os.Stat("/var/run/reboot-required")
	u["rebootPending"] = rr == nil || (modErr != nil && modErr2 != nil)
	return u
}

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func fileTime(p string) string {
	if fi, err := os.Stat(p); err == nil {
		return fi.ModTime().Format("2006-01-02T15:04:05")
	}
	return ""
}

func lastPacmanUpgrade() string {
	b, err := os.ReadFile("/var/log/pacman.log")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "starting full system upgrade") && len(lines[i]) > 20 {
			ts := strings.Trim(strings.Fields(lines[i])[0], "[]")
			for _, layout := range []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z0700", "2006-01-02 15:04"} {
				if t, err := time.Parse(layout, ts); err == nil {
					return t.Format("2006-01-02T15:04:05")
				}
			}
		}
	}
	return ""
}

func scanJournal(ctx context.Context) any {
	ev := map[string]any{}
	type src struct {
		n      int
		sample string
	}
	srcs := map[string]*src{}
	total := 0
	for _, l := range strings.Split(out(ctx, 40*time.Second, "journalctl", "-p", "err", "--since", "7 days ago", "-o", "json", "--no-pager", "-q", "-n", "5000"), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		total++
		str := func(k string) string { v, _ := e[k].(string); return v }
		name := firstNonEmpty(str("_SYSTEMD_UNIT"), str("SYSLOG_IDENTIFIER"), str("_COMM"), "kernel")
		s := srcs[name]
		if s == nil {
			s = &src{}
			srcs[name] = s
		}
		s.n++
		if s.sample == "" {
			if m, ok := e["MESSAGE"].(string); ok {
				s.sample = short(m, 200)
			}
		}
	}
	type kv struct {
		k string
		v *src
	}
	var list []kv
	for k, v := range srcs {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v.n > list[j].v.n })
	var top []map[string]any
	for i, x := range list {
		if i >= 15 {
			break
		}
		top = append(top, map[string]any{"source": x.k, "count": x.v.n, "sample": x.v.sample})
	}
	ev["errorsLast7Days"] = total
	ev["topSources"] = top
	ev["oomKills30d"] = countLines(out(ctx, 30*time.Second, "journalctl", "-k", "--since", "30 days ago", "--no-pager", "-q", "-g", "Out of memory|oom-kill|Killed process"))
	ev["diskErrors30d"] = countLines(out(ctx, 30*time.Second, "journalctl", "-k", "--since", "30 days ago", "--no-pager", "-q", "-g", `I/O error|blk_update_request|ata[0-9.]+: (exception|error)|nvme.*(timeout|error)|Buffer I/O error|EXT4-fs error|BTRFS (error|critical)|XFS.*(error|corruption)`))
	ev["kernelPanicsOrOops30d"] = countLines(out(ctx, 30*time.Second, "journalctl", "-k", "--since", "30 days ago", "--no-pager", "-q", "-g", "Kernel panic|Oops|BUG: |general protection fault|watchdog: BUG"))
	crashes := map[string]int{}
	for _, l := range strings.Split(out(ctx, 30*time.Second, "coredumpctl", "list", "--since", "-30d", "--no-legend", "--no-pager", "-q"), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 {
			crashes[filepath.Base(f[len(f)-2])]++
		}
	}
	var cs []string
	for k, v := range crashes {
		cs = append(cs, fmt.Sprintf("%s x%d", k, v))
	}
	sort.Strings(cs)
	ev["appCrashes30d"] = cs
	ev["bluescreens30d"] = ev["kernelPanicsOrOops30d"]
	return ev
}

var reTime = regexp.MustCompile(`([\d.]+)(min|ms|s)\b`)

func toSec(s string) float64 {
	var t float64
	for _, m := range reTime.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "min":
			t += v * 60
		case "s":
			t += v
		case "ms":
			t += v / 1000
		}
	}
	return math.Round(t*10) / 10
}

func scanBoot(ctx context.Context) any {
	t := out(ctx, 15*time.Second, "systemd-analyze", "time")
	if t == "" {
		return nil
	}
	b := map[string]any{}
	if i := strings.Index(t, "Startup finished in"); i >= 0 {
		line := strings.SplitN(t[i:], "\n", 2)[0]
		total := line
		if j := strings.LastIndex(line, "= "); j >= 0 {
			total = line[j+2:]
		}
		boot := map[string]any{"when": time.Now().Format("2006-01-02T15:04:05"), "bootSec": toSec(total)}
		if j := strings.Index(t, "graphical.target reached after"); j >= 0 {
			boot["toDesktopSec"] = toSec(strings.SplitN(t[j:], "\n", 2)[0])
		}
		b["recentBoots"] = []any{boot}
	}
	var slow []map[string]any
	for _, l := range strings.Split(out(ctx, 15*time.Second, "systemd-analyze", "blame", "--no-pager"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		name := f[len(f)-1]
		sec := toSec(strings.Join(f[:len(f)-1], " "))
		if sec >= 1 {
			slow = append(slow, map[string]any{"kind": "service", "name": name, "worstDelaySec": sec, "times": 1})
		}
		if len(slow) >= 10 {
			break
		}
	}
	b["slowdowns"] = slow
	return b
}

func cpuTimes() (idle, total uint64) {
	f := strings.Fields(strings.SplitN(readStr("/proc/stat"), "\n", 2)[0])
	for i, x := range f[1:] {
		v, _ := strconv.ParseUint(x, 10, 64)
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return
}

func procTimes() map[int][2]any {
	m := map[int][2]any{}
	ds, _ := os.ReadDir("/proc")
	for _, d := range ds {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		s := readStr("/proc/" + d.Name() + "/stat")
		i, j := strings.Index(s, "("), strings.LastIndex(s, ")")
		if i < 0 || j < i {
			continue
		}
		f := strings.Fields(s[j+1:])
		if len(f) < 13 {
			continue
		}
		u, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		m[pid] = [2]any{s[i+1 : j], u + st}
	}
	return m
}

func scanPerf() any {
	i1, t1 := cpuTimes()
	p1 := procTimes()
	time.Sleep(2 * time.Second)
	i2, t2 := cpuTimes()
	p2 := procTimes()
	cpu := 0.0
	if t2 > t1 {
		cpu = math.Round(100 * (1 - float64(i2-i1)/float64(t2-t1)))
	}
	type pc struct {
		n string
		v float64
	}
	var ps []pc
	for pid, b := range p2 {
		if a, ok := p1[pid]; ok && t2 > t1 {
			d := float64(b[1].(uint64)-a[1].(uint64)) / float64(t2-t1) * 100 // share of all CPUs
			if d >= 1 {
				ps = append(ps, pc{b[0].(string), math.Round(d*10) / 10})
			}
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].v > ps[j].v })
	var top []string
	for i, p := range ps {
		if i >= 6 {
			break
		}
		top = append(top, fmt.Sprintf("%s %.1f%%", p.n, p.v))
	}
	mi := meminfo()
	committed := 0.0
	if mi["CommitLimit"] > 0 {
		committed = math.Round(100 * float64(mi["Committed_AS"]) / float64(mi["CommitLimit"]))
	}
	swapPct := 0.0
	if mi["SwapTotal"] > 0 {
		swapPct = math.Round(100 * float64(mi["SwapTotal"]-mi["SwapFree"]) / float64(mi["SwapTotal"]))
	}
	temps := []float64{}
	hs, _ := filepath.Glob("/sys/class/hwmon/hwmon*/temp*_input")
	best := map[string]float64{}
	for _, h := range hs {
		v, err := strconv.ParseFloat(readStr(h), 64)
		chip := readStr(filepath.Join(filepath.Dir(h), "name"))
		if err == nil && v > 0 && v/1000 < 125 && v/1000 > best[chip] {
			best[chip] = v / 1000
		}
	}
	for chip, v := range best {
		if chip != "nvme" && chip != "drivetemp" {
			temps = append(temps, math.Round(v))
		}
	}
	sort.Float64s(temps)
	return map[string]any{"cpuNowPct": cpu, "topCpu": top, "availableMB": mi["MemAvailable"] / 1048576, "committedPct": committed, "swapUsedPct": swapPct, "thermalZonesC": temps}
}

func scanBattery() any {
	bs, _ := filepath.Glob("/sys/class/power_supply/BAT*")
	if len(bs) == 0 {
		return nil
	}
	b := bs[0]
	full, design := readStr(b+"/energy_full"), readStr(b+"/energy_full_design")
	if full == "" {
		full, design = readStr(b+"/charge_full"), readStr(b+"/charge_full_design")
	}
	f, _ := strconv.ParseFloat(full, 64)
	d, _ := strconv.ParseFloat(design, 64)
	m := map[string]any{"cycles": readStr(b + "/cycle_count"), "status": readStr(b + "/status")}
	if d > 0 {
		m["healthPct"] = math.Round(100 * f / d)
	}
	return m
}

func scanSecurity(ctx context.Context) any {
	s := map[string]any{"antivirusProducts": []string{}}
	fw := "none detected"
	if o := out(ctx, 10*time.Second, "ufw", "status"); strings.Contains(o, "Status: active") {
		fw = "ufw active"
	} else if have("firewall-cmd") && strings.TrimSpace(out(ctx, 10*time.Second, "firewall-cmd", "--state")) == "running" {
		fw = "firewalld running"
	} else if o := out(ctx, 10*time.Second, "nft", "list", "ruleset"); strings.Count(o, "\n") > 5 {
		fw = "nftables rules present"
	}
	s["firewall"] = fw
	if o := strings.TrimSpace(out(ctx, 5*time.Second, "getenforce")); o != "" {
		s["selinux"] = o
	}
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err == nil {
		s["apparmor"] = "enabled"
	}
	sshd := strings.ToLower(readStr("/etc/ssh/sshd_config"))
	if sshd != "" {
		s["sshRootLogin"] = regexp.MustCompile(`(?m)^\s*permitrootlogin\s+yes`).MatchString(sshd)
		s["sshPasswordAuth"] = !regexp.MustCompile(`(?m)^\s*passwordauthentication\s+no`).MatchString(sshd)
		s["sshRunning"] = strings.TrimSpace(out(ctx, 5*time.Second, "systemctl", "is-active", "sshd")) == "active" || strings.TrimSpace(out(ctx, 5*time.Second, "systemctl", "is-active", "ssh")) == "active"
	}
	var open []string
	for _, l := range strings.Split(out(ctx, 10*time.Second, "ss", "-H", "-tlnp"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 && (strings.HasPrefix(f[3], "0.0.0.0:") || strings.HasPrefix(f[3], "*:") || strings.HasPrefix(f[3], "[::]:")) {
			proc := ""
			if len(f) >= 6 {
				if m := regexp.MustCompile(`"([^"]+)"`).FindStringSubmatch(f[5]); m != nil {
					proc = m[1]
				}
			}
			open = append(open, strings.TrimSpace(f[3]+" "+proc))
		}
	}
	s["listeningOnNetwork"] = open
	if have("clamscan") {
		s["antivirusProducts"] = []string{"ClamAV"}
	}
	return s
}

func scanBrowsers() any {
	var exts, settings []map[string]any
	roots := map[string]string{"chrome": ".config/google-chrome", "chromium": ".config/chromium", "brave": ".config/BraveSoftware/Brave-Browser", "edge": ".config/microsoft-edge", "vivaldi": ".config/vivaldi"}
	reSearch := regexp.MustCompile(`"default_search_provider_data"\s*:\s*\{\s*"template_url_data"\s*:\s*\{[^}]*?"url"\s*:\s*"([^"]+)"`)
	reHome := regexp.MustCompile(`"homepage"\s*:\s*"([^"]*)"`)
	for _, u := range humans() {
		for b, rel := range roots {
			profs, _ := filepath.Glob(filepath.Join(u.home, rel, "*"))
			for _, prof := range profs {
				base := filepath.Base(prof)
				if base != "Default" && !strings.HasPrefix(base, "Profile ") {
					continue
				}
				ids, _ := os.ReadDir(filepath.Join(prof, "Extensions"))
				for _, id := range ids {
					vers, _ := filepath.Glob(filepath.Join(prof, "Extensions", id.Name(), "*", "manifest.json"))
					if len(vers) == 0 {
						continue
					}
					sort.Strings(vers)
					if name := extName(vers[len(vers)-1]); name != "" {
						exts = append(exts, map[string]any{"browser": b, "user": u.name, "profile": base, "id": id.Name(), "name": short(name, 80)})
					}
				}
				if raw, err := os.ReadFile(filepath.Join(prof, "Preferences")); err == nil {
					var search, home string
					if m := reSearch.FindSubmatch(raw); m != nil {
						search = string(m[1])
					}
					if m := reHome.FindSubmatch(raw); m != nil {
						home = string(m[1])
					}
					if search != "" || home != "" {
						settings = append(settings, map[string]any{"browser": b, "user": u.name, "profile": base, "searchUrl": search, "homepage": home})
					}
				}
			}
		}
		ffs, _ := filepath.Glob(filepath.Join(u.home, ".mozilla/firefox/*/extensions.json"))
		for _, f := range ffs {
			var j struct {
				Addons []struct {
					ID            string `json:"id"`
					Type          string `json:"type"`
					Location      string `json:"location"`
					Active        bool   `json:"active"`
					DefaultLocale struct {
						Name string `json:"name"`
					} `json:"defaultLocale"`
				} `json:"addons"`
			}
			if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &j) == nil {
				for _, a := range j.Addons {
					if a.Type == "extension" && a.Location == "app-profile" {
						exts = append(exts, map[string]any{"browser": "firefox", "user": u.name, "profile": filepath.Base(filepath.Dir(f)), "id": a.ID, "name": short(a.DefaultLocale.Name, 80), "active": a.Active})
					}
				}
			}
		}
	}
	var pols []map[string]any
	for _, d := range browserPolicyPaths() {
		files, _ := filepath.Glob(d)
		for _, f := range files {
			raw, _ := os.ReadFile(f)
			var j map[string]any
			json.Unmarshal(raw, &j)
			p := map[string]any{"key": f}
			if pj := obj(j["policies"]); pj != nil { // Firefox format
				j = pj
			}
			for k := range j {
				p["values"] = append(toStrings(p["values"]), k)
			}
			if fl, ok := j["ExtensionInstallForcelist"].([]any); ok {
				p["forcedExtensions"] = fl
			}
			if v, ok := j["HomepageLocation"].(string); ok {
				p["homepage"] = v
			}
			if v, ok := j["DefaultSearchProviderSearchURL"].(string); ok {
				p["searchUrl"] = v
			}
			pols = append(pols, p)
		}
	}
	var hosts []string
	hostname, _ := os.Hostname()
	for _, l := range strings.Split(readStr("/etc/hosts"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) >= 2 && (f[0] == "127.0.0.1" || f[0] == "127.0.1.1" || f[0] == "::1" || strings.HasPrefix(f[0], "ff0") || strings.HasPrefix(f[0], "fe00")) &&
			(strings.HasPrefix(f[1], "localhost") || strings.HasPrefix(f[1], "ip6-") || f[1] == hostname || strings.HasPrefix(f[1], hostname+".")) {
			continue
		}
		hosts = append(hosts, strings.Join(f, " "))
	}
	var ns []string
	for _, l := range strings.Split(readStr("/etc/resolv.conf"), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "nameserver" {
			ns = append(ns, f[1])
		}
	}
	proxy := map[string]any{}
	for _, l := range strings.Split(readStr("/etc/environment"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.Contains(strings.ToLower(k), "proxy") {
			proxy["server"] = strings.Trim(v, `"'`)
			proxy["enabled"] = 1
		}
	}
	return map[string]any{"extensions": exts, "browserSettings": settings, "browserPolicies": pols, "proxy": proxy,
		"dns": []any{map[string]any{"adapter": "system", "servers": ns, "manual": false}}, "hostsEntries": hosts, "hostsEntryCount": len(hosts)}
}

func toStrings(v any) []string { s, _ := v.([]string); return s }

func browserPolicyPaths() []string {
	return []string{"/etc/opt/chrome/policies/managed/*.json", "/etc/chromium/policies/managed/*.json", "/etc/chromium-browser/policies/managed/*.json",
		"/etc/brave/policies/managed/*.json", "/etc/opt/edge/policies/managed/*.json", "/etc/vivaldi/policies/managed/*.json",
		"/etc/firefox/policies/policies.json", "/usr/lib/firefox/distribution/policies.json", "/usr/lib64/firefox/distribution/policies.json"}
}

func extName(manifest string) string {
	b, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	var m struct {
		Name   string `json:"name"`
		Locale string `json:"default_locale"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	if strings.HasPrefix(m.Name, "__MSG_") {
		key := strings.TrimSuffix(strings.TrimPrefix(m.Name, "__MSG_"), "__")
		for _, loc := range []string{m.Locale, "en", "en_US"} {
			if loc == "" {
				continue
			}
			mb, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), "_locales", loc, "messages.json"))
			if err != nil {
				continue
			}
			var msgs map[string]struct {
				Message string `json:"message"`
			}
			json.Unmarshal(mb, &msgs)
			for k, v := range msgs {
				if strings.EqualFold(k, key) {
					return v.Message
				}
			}
		}
	}
	return m.Name
}

func scanSnapshots(ctx context.Context) any {
	if have("snapper") {
		n := countLines(out(ctx, 15*time.Second, "snapper", "--no-headers", "-c", "root", "list")) // includes "current"
		return map[string]any{"tool": "snapper", "count": max(0, n-1)}
	}
	if have("timeshift") {
		o := out(ctx, 30*time.Second, "timeshift", "--list", "--scripted")
		return map[string]any{"tool": "timeshift", "count": strings.Count(o, " > ")}
	}
	return map[string]any{"tool": "none", "count": 0}
}
