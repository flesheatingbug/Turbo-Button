//go:build linux

package main

// Linux fixes. Each returns a JSON result like the Windows scripts (freedBytes, notes…).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func linuxRun(ctx context.Context, name string, env map[string]string, onLine func(string)) (json.RawMessage, error) {
	var res any
	var err error
	switch name {
	case "scan":
		res = linuxScan(ctx, onLine)
	case "restore_point":
		res, err = actSnapshot(ctx, onLine)
	case "cleanup":
		res, err = actCleanup(ctx, env["PCM_target"], onLine)
	case "orphans_remove":
		res, err = actOrphans(ctx, onLine)
	case "flatpak_unused":
		err = stream(ctx, onLine, nil, "flatpak", "uninstall", "--unused", "-y", "--noninteractive")
		res = map[string]any{"done": err == nil}
	case "updates_check":
		res, err = actUpdatesCheck(ctx, onLine)
	case "system_update":
		res, err = actSystemUpdate(ctx, onLine)
	case "fwupd_update":
		stream(ctx, onLine, nil, "fwupdmgr", "refresh", "--force")
		err = stream(ctx, onLine, nil, "fwupdmgr", "update", "-y", "--no-reboot-check")
		res = map[string]any{"rebootRequired": true, "note": "Some firmware installs during the next restart."}
	case "autostart_disable", "autostart_enable":
		res, err = actAutostart(env["PCM_path"], name == "autostart_disable", onLine)
	case "service_disable":
		err = stream(ctx, onLine, nil, "systemctl", "disable", "--now", env["PCM_name"])
		res = map[string]any{"name": env["PCM_name"], "previousMode": "enabled", "mode": "disabled"}
	case "service_enable":
		err = stream(ctx, onLine, nil, "systemctl", "enable", "--now", env["PCM_name"])
		res = map[string]any{"name": env["PCM_name"], "previousMode": "disabled", "mode": "enabled"}
	case "service_restart":
		err = stream(ctx, onLine, nil, "systemctl", "restart", env["PCM_name"])
		st := strings.TrimSpace(out(ctx, 5*time.Second, "systemctl", "is-active", env["PCM_name"]))
		res = map[string]any{"name": env["PCM_name"], "state": st}
		if err == nil && st != "active" {
			err = fmt.Errorf("%s is %s after restart", env["PCM_name"], st)
		}
	case "failed_reset":
		err = stream(ctx, onLine, nil, "systemctl", "reset-failed")
		res = map[string]any{"done": err == nil}
	case "fstrim":
		err = stream(ctx, onLine, nil, "fstrim", "-av")
		res = map[string]any{"done": err == nil}
	case "smart_test":
		res, err = actSmartTest(ctx, env["PCM_device"], onLine)
	case "problem_check":
		res, err = actProblemCheck(ctx, env["PCM_terms"], onLine)
	case "stress_test":
		res, err = actStressTest(ctx, env, onLine)
	case "disk_usage":
		res = actDiskUsage(onLine)
	case "dns_flush":
		if err = stream(ctx, onLine, nil, "resolvectl", "flush-caches"); err != nil {
			err = stream(ctx, onLine, nil, "systemd-resolve", "--flush-caches")
		}
		res = map[string]any{"flushed": err == nil}
	case "reset_hosts":
		res, err = actResetHosts()
	case "remove_browser_policies":
		res, err = actRemovePolicies(onLine)
	case "restart_pc":
		m := env["PCM_minutes"]
		err = stream(ctx, onLine, nil, "shutdown", "-r", "+"+m, "Turbo Button: restarting in "+m+" minute(s) to finish the repairs. Please save your work.")
		res = map[string]any{"note": "Restarting in " + m + " min. The session ends when the PC restarts."}
	case "shutdown_pc":
		m := env["PCM_minutes"]
		err = stream(ctx, onLine, nil, "shutdown", "-h", "+"+m, "Turbo Button: shutting down in "+m+" minute(s). Please save your work.")
		res = map[string]any{"note": "Shutting down in " + m + " min. The session ends when the PC powers off."}
	case "cancel_restart":
		err = stream(ctx, onLine, nil, "shutdown", "-c")
		res = map[string]any{"note": "Restart or shutdown cancelled."}
	default:
		return nil, fmt.Errorf("%s isn't available on Linux", name)
	}
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(res)
	return b, err
}

// stream runs a command, sending each output line to the live log.
func stream(ctx context.Context, onLine func(string), env []string, name string, args ...string) error {
	if !have(name) {
		return fmt.Errorf("%s is not installed on this PC", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(append(os.Environ(), "LC_ALL=C", "LANG=C"), env...)
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = 15 * time.Second
	pr, pw, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	pw.Close()
	var last []string
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = strings.TrimSpace(l[i+1:])
		}
		if l != "" {
			onLine(l)
			last = append(last, l)
			if len(last) > 4 {
				last = last[1:]
			}
		}
	}
	pr.Close()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s failed: %v — %s", name, err, strings.Join(last, " | "))
	}
	return nil
}

func actSnapshot(ctx context.Context, onLine func(string)) (any, error) {
	switch {
	case have("snapper") && strings.Contains(out(ctx, 10*time.Second, "snapper", "list-configs"), "root"):
		err := stream(ctx, onLine, nil, "snapper", "-c", "root", "create", "-d", "Turbo Button - before changes", "--cleanup-algorithm", "number")
		return map[string]any{"created": err == nil, "tool": "snapper"}, err
	case have("timeshift"):
		err := stream(ctx, onLine, nil, "timeshift", "--create", "--comments", "Turbo Button - before changes", "--scripted")
		return map[string]any{"created": err == nil, "tool": "timeshift"}, err
	}
	return nil, fmt.Errorf("no snapshot tool (Snapper or Timeshift) is set up — continuing without a snapshot")
}

func sizeOf(paths []string) int64 {
	var s int64
	for _, p := range paths {
		s += dirSize(p)
	}
	return s
}

func clearContents(dirs []string, keep func(string) bool) {
	for _, d := range dirs {
		es, _ := os.ReadDir(d)
		for _, e := range es {
			p := filepath.Join(d, e.Name())
			if keep != nil && keep(p) {
				continue
			}
			os.RemoveAll(p)
		}
	}
}

func actCleanup(ctx context.Context, target string, onLine func(string)) (any, error) {
	var dirs []string
	us := humans()
	switch target {
	case "pkg_cache":
		dirs = pkgCacheDirs()
		before := sizeOf(dirs)
		var err error
		switch pkgManager() {
		case "pacman":
			if have("paccache") {
				err = stream(ctx, onLine, nil, "paccache", "-rk1")
				stream(ctx, onLine, nil, "paccache", "-ruk0")
			} else {
				err = stream(ctx, onLine, nil, "pacman", "-Sc", "--noconfirm")
			}
		case "apt-get":
			err = stream(ctx, onLine, nil, "apt-get", "clean")
		case "dnf":
			err = stream(ctx, onLine, nil, "dnf", "clean", "packages")
		case "zypper":
			err = stream(ctx, onLine, nil, "zypper", "clean", "-a")
		default:
			err = fmt.Errorf("unknown package manager")
		}
		return freed(target, before, sizeOf(dirs)), err
	case "journal":
		dirs = []string{"/var/log/journal"}
		before := sizeOf(dirs)
		err := stream(ctx, onLine, nil, "journalctl", "--vacuum-size=200M", "--vacuum-time=4weeks")
		return freed(target, before, sizeOf(dirs)), err
	case "user_cache":
		for _, u := range us {
			dirs = append(dirs, filepath.Join(u.home, ".cache"))
		}
	case "thumbnails":
		for _, u := range us {
			dirs = append(dirs, filepath.Join(u.home, ".cache/thumbnails"))
		}
	case "trash":
		for _, u := range us {
			dirs = append(dirs, filepath.Join(u.home, ".local/share/Trash/files"), filepath.Join(u.home, ".local/share/Trash/info"), filepath.Join(u.home, ".local/share/Trash/expunged"))
		}
	case "coredumps":
		dirs = []string{"/var/lib/systemd/coredump"}
	case "tmp":
		before := oldFilesSize("/var/tmp", 10*24*time.Hour)
		cut := time.Now().Add(-10 * 24 * time.Hour)
		filepath.Walk("/var/tmp", func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && fi.ModTime().Before(cut) {
				os.Remove(p)
			}
			return nil
		})
		return freed(target, before, oldFilesSize("/var/tmp", 10*24*time.Hour)), nil
	default:
		return nil, fmt.Errorf("unknown target %s", target)
	}
	before := sizeOf(dirs)
	onLine(fmt.Sprintf("Clearing %d folder(s)...", len(dirs)))
	clearContents(dirs, nil)
	return freed(target, before, sizeOf(dirs)), nil
}

func freed(target string, before, after int64) map[string]any {
	return map[string]any{"target": target, "freedBytes": before - after, "remainingBytes": after}
}

func actOrphans(ctx context.Context, onLine func(string)) (any, error) {
	switch pkgManager() {
	case "pacman":
		list := strings.Fields(out(ctx, 20*time.Second, "pacman", "-Qtdq"))
		if len(list) == 0 {
			return map[string]any{"removed": 0, "note": "No orphaned packages."}, nil
		}
		err := stream(ctx, onLine, nil, "pacman", append([]string{"-Rns", "--noconfirm"}, list...)...)
		return map[string]any{"removedPackages": list}, err
	case "apt-get":
		err := stream(ctx, onLine, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", "-y", "autoremove", "--purge")
		return map[string]any{"done": err == nil}, err
	case "dnf":
		err := stream(ctx, onLine, nil, "dnf", "-y", "autoremove")
		return map[string]any{"done": err == nil}, err
	}
	return nil, fmt.Errorf("orphan removal isn't supported for %s", pkgManager())
}

func actUpdatesCheck(ctx context.Context, onLine func(string)) (any, error) {
	var pkgs []string
	switch pkgManager() {
	case "pacman":
		if have("checkupdates") {
			pkgs = nonEmpty(out(ctx, 3*time.Minute, "checkupdates"))
		} else {
			stream(ctx, onLine, nil, "pacman", "-Sy")
			pkgs = nonEmpty(out(ctx, time.Minute, "pacman", "-Qu"))
		}
	case "apt-get":
		stream(ctx, onLine, nil, "apt-get", "update", "-q")
		for _, l := range nonEmpty(out(ctx, time.Minute, "apt", "list", "--upgradable")) {
			if strings.Contains(l, "/") {
				pkgs = append(pkgs, l)
			}
		}
	case "dnf":
		for _, l := range nonEmpty(out(ctx, 5*time.Minute, "dnf", "-q", "check-update", "--refresh")) {
			if len(strings.Fields(l)) == 3 {
				pkgs = append(pkgs, l)
			}
		}
	case "zypper":
		stream(ctx, onLine, nil, "zypper", "-q", "refresh")
		for _, l := range nonEmpty(out(ctx, 3*time.Minute, "zypper", "-q", "lu")) {
			if strings.HasPrefix(l, "v ") {
				pkgs = append(pkgs, l)
			}
		}
	}
	for _, p := range pkgs {
		onLine("Update: " + short(p, 100))
	}
	fp := 0
	if have("flatpak") {
		fp = len(nonEmpty(out(ctx, 3*time.Minute, "flatpak", "remote-ls", "--updates", "--columns=application")))
	}
	fw := 0
	if have("fwupdmgr") {
		stream(ctx, func(string) {}, nil, "fwupdmgr", "refresh", "--force")
		var j struct {
			Devices []any `json:"Devices"`
		}
		json.Unmarshal([]byte(out(ctx, 2*time.Minute, "fwupdmgr", "get-updates", "--json")), &j)
		fw = len(j.Devices)
	}
	sample := pkgs
	if len(sample) > 30 {
		sample = sample[:30]
	}
	return map[string]any{"mode": "check", "found": len(pkgs), "packages": sample, "flatpakUpdates": fp, "firmwareUpdates": fw}, nil
}

func nonEmpty(s string) []string {
	var r []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			r = append(r, strings.TrimSpace(l))
		}
	}
	return r
}

func actSystemUpdate(ctx context.Context, onLine func(string)) (any, error) {
	var err error
	switch pkgManager() {
	case "pacman":
		err = stream(ctx, onLine, nil, "pacman", "-Syu", "--noconfirm")
	case "apt-get":
		deb := []string{"DEBIAN_FRONTEND=noninteractive"}
		if err = stream(ctx, onLine, deb, "apt-get", "update", "-q"); err == nil {
			err = stream(ctx, onLine, deb, "apt-get", "-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade", "--with-new-pkgs")
		}
	case "dnf":
		err = stream(ctx, onLine, nil, "dnf", "-y", "upgrade", "--refresh")
	case "zypper":
		if strings.Contains(readKV("/etc/os-release")["ID"], "tumbleweed") {
			err = stream(ctx, onLine, nil, "zypper", "-n", "dup")
		} else {
			err = stream(ctx, onLine, nil, "zypper", "-n", "update")
		}
	default:
		err = fmt.Errorf("no supported package manager found")
	}
	notes := []string{}
	if err == nil && have("flatpak") {
		if e := stream(ctx, onLine, nil, "flatpak", "update", "-y", "--noninteractive"); e != nil {
			notes = append(notes, "Flatpak update failed")
		}
	}
	if err == nil && have("snap") {
		stream(ctx, onLine, nil, "snap", "refresh")
	}
	running := readStr("/proc/sys/kernel/osrelease")
	_, m1 := os.Stat("/usr/lib/modules/" + running)
	_, m2 := os.Stat("/lib/modules/" + running)
	_, rr := os.Stat("/var/run/reboot-required")
	return map[string]any{"mode": "install", "rebootRequired": rr == nil || (m1 != nil && m2 != nil), "note": strings.Join(notes, "; ")}, err
}

const tbMarker = "# Disabled by Turbo Button"

func actAutostart(path string, disable bool, onLine func(string)) (any, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%s not found", path)
	}
	setHidden := func(file string, hidden bool, owner *luser, src string) error {
		var lines []string
		if b, err := os.ReadFile(file); err == nil {
			lines = strings.Split(string(b), "\n")
		} else if src != "" {
			b, _ := os.ReadFile(src)
			lines = strings.Split(string(b), "\n")
		}
		var outL []string
		for _, l := range lines {
			if strings.HasPrefix(l, "Hidden=") || l == tbMarker {
				continue
			}
			outL = append(outL, l)
			if hidden && strings.TrimSpace(l) == "[Desktop Entry]" {
				outL = append(outL, tbMarker, "Hidden=true")
			}
		}
		os.MkdirAll(filepath.Dir(file), 0755)
		if err := os.WriteFile(file, []byte(strings.Join(outL, "\n")), 0644); err != nil {
			return err
		}
		if owner != nil {
			os.Chown(filepath.Dir(file), owner.uid, owner.uid)
			os.Chown(file, owner.uid, owner.uid)
		}
		onLine(fmt.Sprintf("%s: Hidden=%v", file, hidden))
		return nil
	}
	if strings.HasPrefix(path, "/etc/xdg/autostart/") {
		// System entry: per-user override files (the system file is left untouched).
		n := 0
		for _, u := range humans() {
			uu := u
			ov := filepath.Join(u.home, ".config/autostart", filepath.Base(path))
			if !disable {
				if b, err := os.ReadFile(ov); err == nil && strings.Contains(string(b), tbMarker) {
					os.Remove(ov)
					onLine("Removed override " + ov)
					n++
				}
				continue
			}
			if err := setHidden(ov, true, &uu, path); err == nil {
				n++
			}
		}
		if n == 0 && disable {
			return nil, fmt.Errorf("no user accounts found to apply the override to")
		}
		return map[string]any{"path": path, "enabled": !disable, "users": n}, nil
	}
	var owner *luser
	for _, u := range humans() {
		if strings.HasPrefix(path, u.home+"/") {
			uu := u
			owner = &uu
		}
	}
	if err := setHidden(path, disable, owner, ""); err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "enabled": !disable}, nil
}

func actSmartTest(ctx context.Context, dev string, onLine func(string)) (any, error) {
	if !have("smartctl") {
		return nil, fmt.Errorf("smartctl isn't installed (package: smartmontools)")
	}
	if err := stream(ctx, onLine, nil, "smartctl", "-t", "short", dev); err != nil && !strings.Contains(err.Error(), "exit status 4") {
		return nil, err
	}
	onLine("Self-test running (about 2 minutes)...")
	select {
	case <-time.After(150 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var sm map[string]any
	json.Unmarshal([]byte(out(ctx, 30*time.Second, "smartctl", "-j", "-a", dev)), &sm)
	m := map[string]any{"device": dev}
	addSmart(m, sm)
	if m["lastSelfTest"] != nil {
		m["verdict"] = m["lastSelfTest"]
	}
	if n := obj(obj(sm["nvme_self_test_log"])["table"]); n != nil {
		m["verdict"] = obj(n)["self_test_result"]
	}
	return m, nil
}

func actDiskUsage(onLine func(string)) any {
	var targets []string
	for _, u := range humans() {
		es, _ := os.ReadDir(u.home)
		for _, e := range es {
			if e.IsDir() {
				targets = append(targets, filepath.Join(u.home, e.Name()))
			}
		}
		for _, sub := range []string{".local/share", ".cache", ".var/app", ".steam", ".wine"} {
			targets = append(targets, filepath.Join(u.home, sub))
		}
	}
	for _, d := range []string{"/var/lib", "/var/cache", "/var/log", "/opt", "/usr", "/srv", "/var/lib/docker", "/var/lib/flatpak", "/var/lib/snapd"} {
		targets = append(targets, d)
	}
	type f struct {
		Path string  `json:"path"`
		GB   float64 `json:"gb"`
	}
	var res []f
	for _, t := range targets {
		if fi, err := os.Stat(t); err != nil || !fi.IsDir() {
			continue
		}
		onLine("Measuring " + t)
		res = append(res, f{t, gb(dirSize(t))})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].GB > res[j].GB })
	if len(res) > 25 {
		res = res[:25]
	}
	var top []string
	for i, r := range res {
		if i < 3 {
			top = append(top, r.Path+" "+strconv.FormatFloat(r.GB, 'f', 1, 64)+" GB")
		}
	}
	return map[string]any{"folders": res, "note": "Largest: " + strings.Join(top, "; ")}
}

func backupDir() string {
	d := filepath.Join(resumeDir(), "backups")
	os.MkdirAll(d, 0700)
	return d
}

func actResetHosts() (any, error) {
	b, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return nil, err
	}
	bk := filepath.Join(backupDir(), "hosts-"+time.Now().Format("20060102-150405"))
	if err := os.WriteFile(bk, b, 0600); err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	def := "127.0.0.1\tlocalhost\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\t" + host + "\n"
	if err := os.WriteFile("/etc/hosts", []byte(def), 0644); err != nil {
		return nil, err
	}
	return map[string]any{"backup": bk, "removedEntries": len(nonEmpty(string(b))) - 3}, nil
}

func actRemovePolicies(onLine func(string)) (any, error) {
	bk := filepath.Join(backupDir(), "browser-policies-"+time.Now().Format("20060102-150405"))
	var moved []string
	for _, pat := range browserPolicyPaths() {
		files, _ := filepath.Glob(pat)
		for _, f := range files {
			dst := filepath.Join(bk, strings.ReplaceAll(strings.TrimPrefix(f, "/"), "/", "_"))
			os.MkdirAll(bk, 0700)
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if os.WriteFile(dst, b, 0600) == nil && os.Remove(f) == nil {
				moved = append(moved, f)
				onLine("Moved " + f)
			}
		}
	}
	note := "No browser policies found."
	if len(moved) > 0 {
		note = "Restart the browsers. Backups in " + bk
	}
	return map[string]any{"removed": moved, "backups": bk, "note": note}, nil
}
