//go:build linux

package main

// problem_check on Linux: read-only look into the customer's reported problem using keywords
// from the technician's description: matching journal warnings/errors (7 days), processes,
// systemd units and installed packages, plus reachability for network/sign-in problems.

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	errNoTerms = errors.New("no keywords to look for: describe the problem in a few words")
	pidRx      = regexp.MustCompile(`\[\d+\]`)
	digitRx    = regexp.MustCompile(`\d+`)
)

func actProblemCheck(ctx context.Context, terms string, onLine func(string)) (map[string]any, error) {
	var ts []string
	seen := map[string]bool{}
	for _, t := range strings.Fields(strings.ToLower(terms)) {
		if len(t) >= 3 && !seen[t] && len(ts) < 10 {
			seen[t] = true
			ts = append(ts, regexp.QuoteMeta(t))
		}
	}
	if len(ts) == 0 {
		return nil, errNoTerms
	}
	rx := regexp.MustCompile("(?i)" + strings.Join(ts, "|"))
	onLine("Looking for: " + strings.Join(ts, ", "))
	res := map[string]any{"terms": ts}

	onLine("Reading the system journal (7 days)...")
	type grp struct {
		Source string `json:"source"`
		Count  int    `json:"count"`
		Last   string `json:"last"`
		Sample string `json:"message"`
	}
	groups := map[string]*grp{}
	j := out(ctx, 90*time.Second, "journalctl", "--since", "7 days ago", "-p", "warning", "-o", "short-iso", "--no-pager", "-q")
	for _, l := range strings.Split(j, "\n") {
		if !rx.MatchString(l) {
			continue
		}
		f := strings.SplitN(l, " ", 4) // time host source: message
		if len(f) < 4 {
			continue
		}
		src := pidRx.ReplaceAllString(strings.TrimSuffix(f[2], ":"), "")
		msg := strings.TrimSpace(f[3])
		key := src + "|" + digitRx.ReplaceAllString(short(msg, 80), "#")
		g := groups[key]
		if g == nil {
			g = &grp{Source: src}
			groups[key] = g
		}
		g.Count++
		g.Last, g.Sample = f[0], short(msg, 400)
	}
	list := make([]*grp, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Count > list[b].Count })
	if len(list) > 25 {
		list = list[:25]
	}
	res["events"] = list
	onLine(strconv.Itoa(len(list)) + " kind(s) of matching warning/error in the journal.")

	var procs []map[string]any
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		comm := readStr(d + "/comm")
		cmd := strings.ReplaceAll(readStr(d+"/cmdline"), "\x00", " ")
		if comm != "" && (rx.MatchString(comm) || rx.MatchString(cmd)) && len(procs) < 15 {
			procs = append(procs, map[string]any{"name": comm, "pid": filepath.Base(d), "cmd": short(cmd, 200)})
		}
	}
	res["processes"] = procs
	var units []string
	for _, l := range strings.Split(out(ctx, 20*time.Second, "systemctl", "list-units", "--all", "--no-legend", "--plain", "--no-pager"), "\n") {
		if rx.MatchString(l) && len(units) < 15 {
			units = append(units, strings.Join(strings.Fields(l), " "))
		}
	}
	res["units"] = units
	var pkgs []string
	var pl string
	switch pkgManager() {
	case "pacman":
		pl = out(ctx, 30*time.Second, "pacman", "-Q")
	case "apt-get":
		pl = out(ctx, 30*time.Second, "dpkg-query", "-W", "-f", "${Package} ${Version}\n")
	default:
		if have("rpm") {
			pl = out(ctx, 60*time.Second, "rpm", "-qa")
		}
	}
	for _, l := range strings.Split(pl, "\n") {
		if rx.MatchString(l) && len(pkgs) < 20 {
			pkgs = append(pkgs, strings.TrimSpace(l))
		}
	}
	if have("flatpak") {
		for _, l := range strings.Split(out(ctx, 20*time.Second, "flatpak", "list", "--app", "--columns=application,version"), "\n") {
			if rx.MatchString(l) && len(pkgs) < 25 {
				pkgs = append(pkgs, "flatpak "+strings.Join(strings.Fields(l), " "))
			}
		}
	}
	res["packages"] = pkgs
	onLine(strconv.Itoa(len(procs)) + " process(es), " + strconv.Itoa(len(units)) + " unit(s), " + strconv.Itoa(len(pkgs)) + " package(s) match.")

	if regexp.MustCompile(`sign|login|account|internet|network|wifi|dns|email|sync|cloud|update|connect`).MatchString(strings.ToLower(terms)) {
		onLine("Checking the clock and internet reachability...")
		var reach []map[string]any
		for _, h := range []string{"login.microsoftonline.com", "accounts.google.com", "archlinux.org", "1.1.1.1"} {
			t0 := time.Now()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(h, "443"), 4*time.Second)
			r := map[string]any{"host": h, "ok": err == nil}
			if err == nil {
				r["ms"] = time.Since(t0).Milliseconds()
				c.Close()
			}
			reach = append(reach, r)
		}
		res["reachable"] = reach
		res["clock"] = map[string]any{"now": time.Now().Format(time.RFC3339), "timedatectl": short(out(ctx, 10*time.Second, "timedatectl", "show", "-p", "NTPSynchronized", "-p", "Timezone"), 200)}
		res["resolvConf"] = short(readStr("/etc/resolv.conf"), 400)
		if p := os.Getenv("http_proxy") + os.Getenv("https_proxy"); p != "" {
			res["proxyEnv"] = true
		}
	}
	return res, nil
}
