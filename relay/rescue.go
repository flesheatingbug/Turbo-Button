package main

// "Make a rescue USB" on the relay page: hands the technician a small bundle (the Linux agent,
// a launcher, and this relay's settings baked in) to drop onto a SystemRescue / Ventoy USB. When
// a dead Windows PC is booted from that USB, Turbo Button starts in rescue mode and connects back
// to this relay, so the technician can scan the offline Windows from their phone over the PC's WiFi.

import (
	"archive/zip"
	_ "embed"
	"log"
	"net/http"
	"time"
)

//go:embed rescuefiles/make-rescue-usb.sh
var makeUsbSh []byte

//go:embed rescuefiles/make-rescue-usb.ps1
var makeUsbPs1 []byte

const rescueLauncher = `#!/bin/sh
# Turbo Button rescue launcher. Run this after booting the PC from SystemRescue (as root).
# It connects to your relay; scan the offline Windows from your phone.
cd "$(dirname "$0")"
chmod +x ./turbobutton 2>/dev/null
echo "Starting Turbo Button rescue mode..."
exec ./turbobutton --rescue
`

func rescueReadme(pub string) string {
	return "Turbo Button - rescue USB\n" +
		"=========================\n\n" +
		"For a PC that won't boot or keeps blue-screening. Make the stick on any spare computer\n" +
		"with a USB stick (8 GB+). SystemRescue downloads automatically - no other files needed.\n\n" +
		"LINUX (e.g. CachyOS):\n" +
		"  chmod +x make-rescue-usb.sh\n" +
		"  sudo ./make-rescue-usb.sh           # finds the USB, asks you to type ERASE\n" +
		"  (or pass -d /dev/sdX for a specific disk, -i file.iso for your own ISO)\n\n" +
		"WINDOWS:\n" +
		"  Right-click make-rescue-usb.ps1 -> Run with PowerShell (as administrator).\n" +
		"  It lists your USB drives and asks which to use.\n\n" +
		"THEN:\n" +
		"  1. Boot the dead PC from the USB (UEFI or BIOS), connect WiFi (iwctl, or nmtui).\n" +
		"  2. Pick the 'autoruns' boot entry (or run: sh /path/to/turbo-rescue/turbo-rescue.sh).\n" +
		"  3. On your phone, open " + pub + " - the PC shows under Office PCs (relay).\n" +
		"     Run a scan: blue-screen codes, boot/registry health, accounts, SMART. Read-only.\n\n" +
		"Nothing on the Windows disk is changed unless you choose a fix.\n"
}

func (r *Relay) uiRescue(w http.ResponseWriter, req *http.Request) {
	if !r.loggedIn(req) {
		http.Error(w, "Sign in to the relay page first.", 401)
		return
	}
	r.mu.Lock()
	conf := "relay=" + r.publicURL(req) + "\nrelaykey=" + r.cfg.AgentKey + "\nstandby=yes\n"
	pub := r.publicURL(req)
	r.mu.Unlock()

	// A small, reliable zip: the maker scripts, a launcher, your relay settings, and a README.
	// The scripts download the Linux agent from GitHub themselves when you build the stick, so
	// this download never depends on the relay reaching GitHub.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="turbo-button-rescue.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	add := func(name string, body []byte) {
		h := &zip.FileHeader{Name: "turbo-rescue/" + name, Method: zip.Deflate, Modified: time.Now()}
		h.SetMode(0755)
		fw, err := zw.CreateHeader(h)
		if err == nil {
			fw.Write(body)
		}
	}
	add("turbobutton.conf", []byte(conf))
	add("turbo-rescue.sh", []byte(rescueLauncher))
	add("make-rescue-usb.sh", makeUsbSh)
	add("make-rescue-usb.ps1", makeUsbPs1)
	add("README.txt", []byte(rescueReadme(pub)))
	zw.Close()
	log.Printf("rescue maker downloaded from %s", r.clientIP(req))
}
