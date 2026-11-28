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
		"1. Put a SystemRescue ISO on your Ventoy/Medicat USB (https://www.system-rescue.org).\n" +
		"2. Copy this whole folder onto the USB's data partition.\n" +
		"   Or make a STANDALONE stick (no Ventoy):\n" +
		"     Linux:   sudo ./make-rescue-usb.sh -d /dev/sdX   (downloads SystemRescue for you)\n" +
		"     Windows: run make-rescue-usb.ps1 in an admin PowerShell (UEFI boot)\n" +
		"3. Boot the dead PC from SystemRescue, connect WiFi (iwctl, or nmtui), become root.\n" +
		"4. Pick the 'autoruns' boot entry, or run:  sh /path/to/turbo-rescue.sh\n" +
		"5. On your phone, open " + pub + " , sign in, and the PC appears under Office PCs (relay).\n" +
		"   Run a scan: it reads the offline Windows (blue-screen codes, boot/registry health, SMART).\n\n" +
		"Nothing on the Windows disk is changed - the scan is read-only.\n"
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
