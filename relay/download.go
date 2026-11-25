package main

// "Download Turbo Button" on the relay page: fetches the latest build from GitHub (checked
// against SHA256SUMS), and hands it out with this relay's settings built in, so a PC needs no
// turbobutton.conf. Windows: the settings are appended to TurboButton.exe (the agent reads them
// from its own file and keeps them across self-updates). Linux: a zip with the AppImage and
// turbobutton.conf.

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const confMagic = "TBCONF01"

var dlMu sync.Mutex

func releaseBase() string {
	return strings.TrimRight(envOr("RELEASE_URL", "https://github.com/flesheatingbug/Turbo-Button/releases/download/latest"), "/")
}

// cachedAsset returns a verified local copy of a release file, downloading it when the
// published checksum changes.
func (r *Relay) cachedAsset(name string) (string, error) {
	dlMu.Lock()
	defer dlMu.Unlock()
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(releaseBase() + "/SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("couldn't reach GitHub: %v", err)
	}
	want := ""
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 64*1024))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || len(want) != 64 {
		return "", fmt.Errorf("the latest release has no checksum for %s (GitHub answered %d)", name, resp.StatusCode)
	}
	dir := filepath.Join(r.dir, "cache")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, name+"."+want[:16])
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	// Remove older cached copies of this file.
	if old, _ := filepath.Glob(filepath.Join(dir, name+".*")); len(old) > 0 {
		for _, o := range old {
			os.Remove(o)
		}
	}
	dl := &http.Client{Timeout: 5 * time.Minute}
	resp, err = dl.Get(releaseBase() + "/" + name)
	if err != nil {
		return "", fmt.Errorf("download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub answered %d for %s", resp.StatusCode, name)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		os.Remove(tmp)
		return "", fmt.Errorf("checksum mismatch for %s, download discarded", name)
	}
	return path, os.Rename(tmp, path)
}

func (r *Relay) uiDownload(w http.ResponseWriter, req *http.Request) {
	if !r.loggedIn(req) {
		http.Error(w, "Sign in to the relay page first.", 401)
		return
	}
	standby := req.URL.Query().Get("standby") == "1"
	r.mu.Lock()
	conf := "relay=" + r.publicURL(req) + "\nrelaykey=" + r.cfg.AgentKey + "\n"
	r.mu.Unlock()
	if standby {
		conf += "standby=yes\n"
	}
	switch req.URL.Query().Get("os") {
	case "linux":
		p, err := r.cachedAsset("TurboButton-x86_64.AppImage")
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		name := "TurboButton-linux"
		if standby {
			name += "-office"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		zw := zip.NewWriter(w)
		hdr := &zip.FileHeader{Name: "TurboButton-x86_64.AppImage", Method: zip.Deflate, Modified: time.Now()}
		hdr.SetMode(0755)
		if zf, err := zw.CreateHeader(hdr); err == nil {
			if f, err := os.Open(p); err == nil {
				io.Copy(zf, f)
				f.Close()
			}
		}
		ch := &zip.FileHeader{Name: "turbobutton.conf", Method: zip.Deflate, Modified: time.Now()}
		ch.SetMode(0600)
		if zf, err := zw.CreateHeader(ch); err == nil {
			io.WriteString(zf, conf)
		}
		zw.Close()
	default:
		p, err := r.cachedAsset("TurboButton.exe")
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		fi, _ := f.Stat()
		tail := make([]byte, 4)
		binary.LittleEndian.PutUint32(tail, uint32(len(conf)))
		trailer := append(append([]byte(conf), tail...), confMagic...)
		name := "TurboButton.exe"
		if standby {
			name = "TurboButton-office.exe"
		}
		w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Content-Length", fmt.Sprint(fi.Size()+int64(len(trailer))))
		w.Header().Set("Cache-Control", "no-store")
		io.Copy(w, f)
		w.Write(trailer)
	}
	log.Printf("download: %s (standby=%v) from %s", req.URL.Query().Get("os"), standby, r.clientIP(req))
}
