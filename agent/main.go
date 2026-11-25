package main

import (
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const port = 47800

func main() {
	vt := setupConsole()
	if selfUpdate() {
		return
	}
	if !isAdmin() {
		fmt.Println("Turbo Button needs administrator rights. Asking Windows to restart it as administrator...")
		if err := relaunchAsAdmin(); err != nil {
			fmt.Println("Could not get administrator rights:", err)
			if hasArg("--gui") {
				desktopAlert("Turbo Button needs administrator rights to service this PC: " + err.Error() + ".")
				return
			}
			waitEnter()
		}
		return
	}

	a, err := NewAgent()
	if err != nil {
		fmt.Println("Startup failed:", err)
		if hasArg("--gui") {
			desktopAlert("Turbo Button couldn't start: " + err.Error())
			return
		}
		waitEnter()
		return
	}
	a.machineID = machineID()
	listenPort := port
	var resumeErr error
	a.relayConf = loadRelayConf()
	if a.relayConf.Standby && a.relayConf.on() {
		// Standby: same session key every run, so the phone that paired once can reconnect.
		if t := loadStandbyToken(); t != "" {
			a.token = t
		} else {
			saveStandbyToken(a.token)
		}
	}
	if hasArg("--resume") {
		var st *resumeState
		if st, resumeErr = loadResume(); resumeErr == nil {
			if !a.relayConf.on() && st.Relay.on() {
				a.relayConf = st.Relay
			}
			a.restoreFrom(st)
			if st.Port > 0 {
				listenPort = st.Port
			}
		}
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", listenPort))
	if err != nil {
		ln, err = net.Listen("tcp", "0.0.0.0:0")
	}
	if err != nil {
		fmt.Println("Could not open a network port:", err)
		a.Cleanup()
		waitEnter()
		return
	}
	p := ln.Addr().(*net.TCPAddr).Port
	a.port = p
	addFirewallRule(p)

	cleanup := func(byPhone bool) {
		a.mu.Lock()
		armed := a.resumeArmed
		a.mu.Unlock()
		if armed && !byPhone {
			// Windows is closing us for the restart we started: keep the resume files.
			fmt.Println("\n  > RESTARTING — Turbo Button will reopen after you log back in.")
			closeScreen(a.workDir)
			removeFirewallRule()
			a.Cleanup()
			return
		}
		fmt.Println("\n  > ENDING SESSION: removing firewall rule and temporary files...")
		if a.relay != nil {
			a.relay.Close()
		}
		closeScreen(a.workDir)
		removeFirewallRule()
		cancelResume()
		removeAgentCopy()
		a.Cleanup()
		fmt.Println("  > DONE. Nothing from Turbo Button is left on this PC (any restore point and approved changes stay).")
		if vt {
			fmt.Print("\x1b[0m")
		}
	}

	con := &Console{color: vt}
	a.OnEvent = con.Handle

	handler := a.Handler()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	if a.relayConf.on() {
		a.relay = a.startRelay(a.relayConf, handler)
	}

	ips := lanIPs()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	url := fmt.Sprintf("http://%s:%d/#t=%s", ips[0], p, a.token)
	if a.relayConf.on() {
		url += "&r=" + neturl.QueryEscape(a.relayConf.URL)
	}
	stopAwake := keepAwake()
	defer stopAwake()
	a.controlURL = url
	if q, err := EncodeQR(url); err == nil {
		a.qrSVG = q.SVG()
	}
	printBanner(url, ips, p, vt)
	if !hasArg("--no-screen") {
		screenURL := fmt.Sprintf("http://127.0.0.1:%d/screen#v=%s", p, a.viewToken)
		if mockMode() {
			fmt.Println("  SCREEN: " + screenURL)
		}
		if err := openScreen(screenURL, a.workDir); err == nil {
			fmt.Println(con.paint(cDim, "  > Customer screen opened — this console hides itself. Closing the screen ends the session."))
			if !hasArg("--console") {
				time.Sleep(1500 * time.Millisecond)
				if hideConsole() {
					go a.screenWatchdog()
				}
			}
		} else if hasArg("--gui") {
			// Started without a terminal: with no screen there'd be no QR code anywhere.
			desktopAlert("Turbo Button couldn't open its screen (" + err.Error() + "). Install Chromium, Chrome, Brave or Vivaldi, or run it from a terminal with --console.")
			srv.Close()
			cleanup(false)
			return
		}
	}
	if a.resumed {
		fmt.Println(con.paint(cPink, "  > SESSION RESUMED AFTER RESTART — the phone reconnects by itself. Rescan to check the results."))
	} else if resumeErr != nil {
		fmt.Println(con.paint(cYellow, "  > Could not resume the previous session ("+resumeErr.Error()+") — starting a new one."))
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP) // SIGHUP: the terminal window was closed
	byPhone := false
	select {
	case <-sig:
	case <-a.finishing:
		byPhone = true
	}
	srv.Close()
	cleanup(byPhone)
	if !mockMode() {
		time.Sleep(2 * time.Second)
	}
}

var (
	bigRow1 = "▀█▀ █ █ █▀█ █▄▄ █▀█   █▄▄ █ █ ▀█▀ ▀█▀ █▀█ █▄ █"
	bigRow2 = " █  █▄█ █▀▄ █▄█ █▄█   █▄█ █▄█  █   █  █▄█ █ ▀█"
)

func pad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func dots(label, value string) string {
	n := 30 - utf8.RuneCountInString(label)
	if n < 2 {
		n = 2
	}
	return "  > " + label + " " + strings.Repeat(".", n) + " " + value
}

func printBanner(url string, ips []string, port int, color bool) {
	g, hi, rs := "", "", ""
	if color {
		g, hi, rs = "\x1b[32m", "\x1b[1;92m", "\x1b[0m\x1b[32m"
	}
	const w = 54
	fmt.Print(g)
	fmt.Println(" ╔" + strings.Repeat("═", w) + "╗")
	fmt.Println(" ║  " + hi + pad(bigRow1, w-2) + rs + "║")
	fmt.Println(" ║  " + hi + pad(bigRow2, w-2) + rs + "║")
	fmt.Println(" ║  " + pad(fmt.Sprintf("TECHNICIAN TERMINAL v%s", Version), w-17) + "[■] TURBO ON   ║")
	fmt.Println(" ╚" + strings.Repeat("═", w) + "╝")
	for _, l := range [][2]string{{"POWER-ON SELF TEST", "OK"}, {"ADMINISTRATOR RIGHTS", "OK"},
		{"NETWORK LINK", fmt.Sprintf("%s:%d", ips[0], port)}, {"KEEP-AWAKE", "ON"}, {"SESSION KEY", "GENERATED"}} {
		fmt.Println(dots(l[0], hi+l[1]+rs))
		if !mockMode() {
			time.Sleep(60 * time.Millisecond)
		}
	}
	fmt.Println()
	if q, err := EncodeQR(url); err == nil {
		// QR codes need real black/white, not green.
		if color {
			fmt.Print("\x1b[0m")
		}
		fmt.Print(q.Terminal())
		fmt.Print(g)
	}
	fmt.Println("  SCAN THE CODE WITH THE TURBO BUTTON APP (same network as this PC)")
	fmt.Println("  or open: " + hi + url + rs)
	if len(ips) > 1 {
		fmt.Println("  Other addresses for this PC (use the same #t=... part):")
		for _, ip := range ips[1:] {
			fmt.Printf("    http://%s:%d/\n", ip, port)
		}
	}
	fmt.Println()
	fmt.Println("  Close this window or press Ctrl+C to end the session and clean up.")
	fmt.Println(" " + strings.Repeat("─", w+2))
	fmt.Println("  " + hi + "LIVE ACTIVITY" + rs)
	fmt.Println(" " + strings.Repeat("─", w+2))
}

// lanIPs returns private IPv4 addresses, the one with the default route first.
func lanIPs() []string {
	pref := ""
	if c, err := net.Dial("udp", "192.0.2.1:9"); err == nil { // no packets are sent for UDP dial
		pref = c.LocalAddr().(*net.UDPAddr).IP.String()
		c.Close()
	}
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifc.Name)
		if strings.Contains(name, "vethernet") || strings.Contains(name, "virtualbox") || strings.Contains(name, "vmware") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			ips = append(ips, ipn.IP.String())
		}
	}
	sort.SliceStable(ips, func(i, j int) bool { return ips[i] == pref && ips[j] != pref })
	if pref != "" && net.ParseIP(pref).IsPrivate() && (len(ips) == 0 || ips[0] != pref) {
		ips = append([]string{pref}, ips...)
	}
	return ips
}

// screenWatchdog brings the console back if the screen window disappeared without saying so
// (e.g. it crashed) and the phone isn't around either, so the session can still be ended.
func (a *Agent) screenWatchdog() {
	for range time.Tick(20 * time.Second) {
		a.mu.Lock()
		lost := !a.screenSeen.IsZero() && time.Since(a.screenSeen) > 3*time.Minute && time.Since(a.lastSeen) > 3*time.Minute
		a.mu.Unlock()
		if lost {
			showConsole()
			fmt.Println("\n  > The customer screen was closed. Close this window (or press End on the phone) to end the session.")
			return
		}
	}
}

func hasArg(a string) bool {
	for _, x := range os.Args[1:] {
		if x == a {
			return true
		}
	}
	return false
}

func waitEnter() {
	fmt.Println("Press Enter to close.")
	var b [1]byte
	os.Stdin.Read(b[:])
}
