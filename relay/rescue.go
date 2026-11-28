package main

// "Make a rescue USB" on the relay page: hands the technician a small bundle (the Linux agent,
// a launcher, and this relay's settings baked in) to drop onto a SystemRescue / Ventoy USB. When
// a dead Windows PC is booted from that USB, Turbo Button starts in rescue mode and connects back
// to this relay, so the technician can scan the offline Windows from their phone over the PC's WiFi.

import (
	"archive/zip"
	"embed"
	"io/fs"
	"strings"
	"log"
	"net/http"
	"time"
)

//go:embed rescuefiles/make-rescue-usb.sh
var makeUsbSh []byte

//go:embed rescuefiles/make-rescue-usb.ps1
var makeUsbPs1 []byte

//go:embed rescuefiles/build-rescue-iso.sh
var buildIsoSh []byte

//go:embed rescuefiles/turbo-iso
var turboIsoFS embed.FS

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
		"For a PC that won't boot or keeps blue-screening. The stick boots STRAIGHT into Turbo\n" +
		"Button - no full rescue OS. Build it on an Arch/CachyOS box with a spare USB (8 GB+):\n\n" +
		"  chmod +x build-rescue-iso.sh\n" +
		"  sudo ./build-rescue-iso.sh          # builds the image + writes the USB (type ERASE)\n\n" +
		"Your relay settings (turbobutton.conf, included here) are baked in, so the booted PC\n" +
		"connects to " + pub + " by itself and appears under Office PCs (relay).\n\n" +
		"On the dead PC: boot from the USB (BIOS or UEFI), connect WiFi when asked. You're in\n" +
		"Turbo Button. Run a scan: blue-screen codes, boot/registry health, accounts, SMART.\n" +
		"Read-only - nothing on the Windows disk changes unless you choose a fix.\n\n" +
		"Not on Arch? Build the ISO on any Arch/CachyOS machine (--iso-only), then write out/*.iso\n" +
		"to USB anywhere with dd (or Rufus/Etcher). make-rescue-usb.sh/.ps1 build a full\n" +
		"SystemRescue stick instead, if you prefer that.\n"
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
	add("build-rescue-iso.sh", buildIsoSh)
	// The archiso profile for the minimal boot-straight-to-Turbo-Button image.
	fs.WalkDir(turboIsoFS, "rescuefiles/turbo-iso", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, e := turboIsoFS.ReadFile(path); e == nil {
			add(strings.TrimPrefix(path, "rescuefiles/"), b)
		}
		return nil
	})
	add("README.txt", []byte(rescueReadme(pub)))
	zw.Close()
	log.Printf("rescue maker downloaded from %s", r.clientIP(req))
}
