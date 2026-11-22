package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

const port = 47800

func main() {
	setupConsole()
	if !isAdmin() {
		fmt.Println("Turbo Button needs administrator rights. Asking Windows to restart it as administrator...")
		if err := relaunchAsAdmin(); err != nil {
			fmt.Println("Could not get administrator rights:", err)
			waitEnter()
		}
		return
	}

	a, err := NewAgent()
	if err != nil {
		fmt.Println("Startup failed:", err)
		waitEnter()
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
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
	addFirewallRule(p)

	cleanup := func() {
		fmt.Println("\nEnding session: removing firewall rule and temporary files...")
		removeFirewallRule()
		a.Cleanup()
		fmt.Println("Done. Nothing from Turbo Button is left on this PC (any restore point and approved changes stay).")
	}

	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)

	ips := lanIPs()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	url := fmt.Sprintf("http://%s:%d/#t=%s", ips[0], p, a.token)
	printBanner(url, ips, p)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		t := time.NewTicker(time.Second)
		shown := false
		for range t.C {
			a.mu.Lock()
			seen := a.phoneSeen
			a.mu.Unlock()
			if seen && !shown {
				shown = true
				fmt.Println(">> Phone connected. Control the session from the phone.")
			}
		}
	}()
	select {
	case <-sig:
	case <-a.finishing:
	}
	srv.Close()
	cleanup()
	if !mockMode() {
		time.Sleep(2 * time.Second)
	}
}

func printBanner(url string, ips []string, port int) {
	fmt.Println("==============================================================")
	fmt.Println("   ▄▄▄ T U R B O   B U T T O N ▄▄▄   [●] TURBO ON    v" + Version)
	fmt.Println("==============================================================")
	if q, err := EncodeQR(url); err == nil {
		fmt.Print(q.Terminal())
	}
	fmt.Println("Scan the QR code with your phone (same Wi-Fi / network as this PC).")
	fmt.Println("Or open this address on the phone:")
	fmt.Println("  " + url)
	if len(ips) > 1 {
		fmt.Println("Other addresses for this PC (use the same #t=... part):")
		for _, ip := range ips[1:] {
			fmt.Printf("  http://%s:%d/\n", ip, port)
		}
	}
	fmt.Println()
	fmt.Println("Close this window or press Ctrl+C to end the session and clean up.")
	fmt.Println("--------------------------------------------------------------")
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

func waitEnter() {
	fmt.Println("Press Enter to close.")
	var b [1]byte
	os.Stdin.Read(b[:])
}
