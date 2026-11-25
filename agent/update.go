package main

// Self-update on launch: if the GitHub "latest" release has a newer build than this one, download
// it, check its SHA-256, put it in place of this file (so the USB stick stays current) and run it.
// No internet, a slow network or any error simply carries on with the current version.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Build is the CI run number, set at build time (-ldflags "-X main.Build=N"). "0" = a local build: no self-update.
var Build = "0"

var releaseAPI = envOr("TURBO_RELEASE_API", "https://api.github.com/repos/flesheatingbug/Turbo-Button/releases/tags/latest")

// updateTarget is the release asset that replaces this program, and the file it lives in.
func updateTarget() (asset, path string) {
	switch runtime.GOOS {
	case "windows":
		p, _ := os.Executable()
		return "TurboButton.exe", p
	case "linux":
		if ai := os.Getenv("APPIMAGE"); ai != "" {
			return "TurboButton-x86_64.AppImage", ai
		}
		arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
		if arch == "" {
			return "", ""
		}
		p, _ := os.Executable()
		return "TurboButton-linux-" + arch, p
	}
	return "", ""
}

// selfUpdate returns true when a newer build was started (and has finished): the caller should exit.
func selfUpdate() bool {
	cur, _ := strconv.Atoi(Build)
	if cur == 0 || mockMode() || hasArg("--no-update") || hasArg("--updated") || hasArg("--resume") || hasArg("--gui-user") || os.Getenv("TURBO_NO_UPDATE") != "" {
		return false
	}
	asset, target := updateTarget()
	if asset == "" || target == "" {
		return false
	}
	if p, err := filepath.EvalSymlinks(target); err == nil {
		target = p
	}
	cleanupOld(target)

	fmt.Print("Checking for a newer Turbo Button... ")
	rel, err := latestRelease()
	if err != nil {
		fmt.Println("skipped (" + err.Error() + ")")
		return false
	}
	if rel.build <= cur {
		fmt.Printf("up to date (build %d)\n", cur)
		return false
	}
	url, sumURL := rel.assets[asset], rel.assets["SHA256SUMS"]
	if url == "" || sumURL == "" {
		fmt.Println("a new build exists but has no download for this system yet")
		return false
	}
	fmt.Printf("build %d is available (you have %d). Downloading...\n", rel.build, cur)
	newPath, err := downloadVerified(url, sumURL, asset, target)
	if err != nil {
		fmt.Println("Update failed, carrying on with this version:", err)
		return false
	}
	// Keep relay settings that were built into this copy (from the relay's download button).
	if conf := embeddedConf(target); conf != "" {
		if err := appendConf(newPath, conf); err != nil {
			os.Remove(newPath)
			fmt.Println("Update failed, carrying on with this version: couldn't copy the built-in relay settings:", err)
			return false
		}
	}
	final, err := swapIn(newPath, target)
	if err != nil {
		fmt.Println("Couldn't replace the old file (" + err.Error() + "); running the new build from a temporary copy.")
		final = newPath
	} else {
		fmt.Println("Updated", filepath.Base(final), "to build", rel.build)
	}
	runUpdated(final, final == newPath)
	return true
}

type release struct {
	build  int
	assets map[string]string
}

func latestRelease() (*release, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", releaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "turbobutton-agent")
	r, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no internet")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered %d", r.StatusCode)
	}
	var j struct {
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&j); err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`[Bb]uild (\d+)`).FindStringSubmatch(j.Body)
	if m == nil {
		return nil, fmt.Errorf("no build number in the release")
	}
	rel := &release{assets: map[string]string{}}
	rel.build, _ = strconv.Atoi(m[1])
	for _, a := range j.Assets {
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
}

func downloadVerified(url, sumURL, asset, target string) (string, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	r, err := c.Get(sumURL)
	if err != nil {
		return "", err
	}
	want := ""
	sc := bufio.NewScanner(io.LimitReader(r.Body, 64*1024))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = strings.ToLower(f[0])
		}
	}
	r.Body.Close()
	if len(want) != 64 {
		return "", fmt.Errorf("no checksum published for %s", asset)
	}

	// Download next to the old file (same drive, so it can be swapped in); fall back to the temp folder.
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".turbobutton-update-*")
	if err != nil {
		if f, err = os.CreateTemp("", "turbobutton-update-*"); err != nil {
			return "", err
		}
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	dl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := dl.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download answered %d", resp.StatusCode)
	}
	h := sha256.New()
	total, done, shown := resp.ContentLength, int64(0), -1
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if total > 0 {
				if pct := int(done * 100 / total); pct/10 != shown/10 {
					shown = pct
					fmt.Printf("\r  %3d%%", pct)
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	fmt.Println()
	if err := f.Close(); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("checksum mismatch, download discarded")
	}
	os.Chmod(tmp, 0755)
	ok = true
	return tmp, nil
}

// swapIn puts the new file where the old one was. A running .exe can't be overwritten on Windows,
// but it can be renamed, so the old one becomes TurboButton.old.exe and is removed next launch.
func swapIn(newPath, target string) (string, error) {
	if filepath.Dir(newPath) != filepath.Dir(target) {
		return "", fmt.Errorf("the folder isn't writable")
	}
	if runtime.GOOS == "windows" {
		old := strings.TrimSuffix(target, filepath.Ext(target)) + ".old" + filepath.Ext(target)
		os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return "", err
		}
		if err := os.Rename(newPath, target); err != nil {
			os.Rename(old, target)
			return "", err
		}
		return target, nil
	}
	if fi, err := os.Stat(target); err == nil {
		os.Chmod(newPath, fi.Mode().Perm()|0111)
	}
	if err := os.Rename(newPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func cleanupOld(target string) {
	if runtime.GOOS == "windows" {
		os.Remove(strings.TrimSuffix(target, filepath.Ext(target)) + ".old" + filepath.Ext(target))
	}
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".turbobutton-update-*")); len(m) > 0 {
		for _, p := range m {
			if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > time.Hour {
				os.Remove(p)
			}
		}
	}
}

// runUpdated starts the new build with the same arguments and waits for it, so the console window
// and Ctrl+C behave exactly as before. A temporary copy is deleted afterwards.
func runUpdated(path string, temporary bool) {
	args := append(append([]string{}, os.Args[1:]...), "--updated")
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sig { // the new build gets these too and cleans up; just wait for it
		}
	}()
	if err := cmd.Run(); err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			fmt.Println("Couldn't start the new build:", err)
			waitEnter()
		}
	}
	signal.Stop(sig)
	if temporary {
		os.Remove(path)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
