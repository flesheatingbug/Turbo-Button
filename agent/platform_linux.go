//go:build linux

package main

// Linux platform layer: elevation with pkexec/sudo, the customer screen opened as the
// logged-in user (never as root), temporary firewall openings, and sleep inhibition.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setupConsole() bool     { return true }
func powershellPath() string { return "pwsh" }
func hideWindow(*exec.Cmd)   {}
func hideConsole() bool      { return false }
func showConsole()           {}

func isAdmin() bool { return mockMode() || os.Geteuid() == 0 }

func have(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }

// relaunchAsAdmin re-runs the agent as root and waits for it. The binary is copied out of
// the AppImage mount first (root can't read another user's FUSE mount), and the desktop
// session details are passed along so root can open the customer screen as this user.
func relaunchAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	b := make([]byte, 6)
	rand.Read(b)
	dir := filepath.Join(os.TempDir(), "turbobutton-launch-"+hex.EncodeToString(b))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	copyExe := filepath.Join(dir, "turbobutton")
	if err := copyFile(exe, copyExe); err != nil {
		return err
	}
	os.Chmod(copyExe, 0755)
	u, _ := user.Current()
	args := []string{copyExe, "--gui-user=" + u.Username, "--gui-uid=" + u.Uid}
	for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY", "XDG_SESSION_TYPE"} {
		if v := os.Getenv(k); v != "" {
			args = append(args, "--gui-env="+k+"="+v)
		}
	}
	for _, a := range os.Args[1:] {
		args = append(args, a)
	}
	var cmd *exec.Cmd
	switch {
	case have("pkexec") && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""):
		cmd = exec.Command("pkexec", args...)
	case have("sudo"):
		cmd = exec.Command("sudo", args...)
	case have("pkexec"):
		cmd = exec.Command("pkexec", args...)
	default:
		return fmt.Errorf("run it as root (sudo ./TurboButton)")
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl+C and closing the terminal reach the admin copy too, and it cleans up and exits.
	// This launcher must not die first: wait for it, so the terminal doesn't report a crash
	// and the temporary copy below is removed.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sig)
	go func() {
		for range sig {
		}
	}()
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		switch ee.ExitCode() {
		case 126, 127: // pkexec: authentication cancelled or not authorised
			return fmt.Errorf("the password prompt was cancelled")
		default:
			return nil // the admin copy ran and has already reported anything that went wrong
		}
	}
	return err
}

// ---- who is the desktop user (for the customer screen) ----

type guiUser struct {
	name string
	uid  int
	env  []string
}

func desktopUser() *guiUser {
	g := &guiUser{uid: -1}
	for _, a := range os.Args[1:] {
		switch {
		case strings.HasPrefix(a, "--gui-user="):
			g.name = strings.TrimPrefix(a, "--gui-user=")
		case strings.HasPrefix(a, "--gui-uid="):
			g.uid, _ = strconv.Atoi(strings.TrimPrefix(a, "--gui-uid="))
		case strings.HasPrefix(a, "--gui-env="):
			g.env = append(g.env, strings.TrimPrefix(a, "--gui-env="))
		}
	}
	if g.name == "" { // started with plain sudo/pkexec from a terminal
		if n := os.Getenv("SUDO_USER"); n != "" {
			g.name = n
		} else if id := os.Getenv("PKEXEC_UID"); id != "" {
			if u, err := user.LookupId(id); err == nil {
				g.name = u.Username
			}
		}
	}
	if g.name == "" {
		return nil
	}
	if g.uid < 0 {
		if u, err := user.Lookup(g.name); err == nil {
			g.uid, _ = strconv.Atoi(u.Uid)
		}
	}
	if len(g.env) == 0 { // best guess for a standard desktop session
		rt := fmt.Sprintf("/run/user/%d", g.uid)
		g.env = []string{"XDG_RUNTIME_DIR=" + rt, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + rt + "/bus"}
		if v := os.Getenv("DISPLAY"); v != "" {
			g.env = append(g.env, "DISPLAY="+v)
		} else {
			g.env = append(g.env, "DISPLAY=:0")
		}
		if v := os.Getenv("WAYLAND_DISPLAY"); v != "" {
			g.env = append(g.env, "WAYLAND_DISPLAY="+v)
		}
	}
	return g
}

// openScreen opens the customer screen in an app-style Chromium-family window as the desktop user.
func openScreen(url, workDir string) error {
	if mockMode() {
		return fmt.Errorf("mock")
	}
	g := desktopUser()
	if g == nil || !have("runuser") {
		return fmt.Errorf("no desktop user")
	}
	prof := filepath.Join(os.TempDir(), "turbobutton-screen-"+filepath.Base(workDir))
	os.MkdirAll(prof, 0700)
	os.Chown(prof, g.uid, g.uid)
	env := append([]string{"HOME=" + homeOf(g.name)}, g.env...)
	for _, b := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome", "brave-browser", "brave", "microsoft-edge-stable", "microsoft-edge", "vivaldi-stable", "vivaldi"} {
		if !have(b) {
			continue
		}
		args := append([]string{"-u", g.name, "--", "env"}, env...)
		args = append(args, b, "--app="+url, "--user-data-dir="+prof, "--window-size=1100,780", "--no-first-run", "--no-default-browser-check")
		c := exec.Command("runuser", args...)
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err == nil {
			go c.Wait()
			return nil
		}
	}
	if have("xdg-open") { // ordinary browser tab as a fallback
		args := append([]string{"-u", g.name, "--", "env"}, env...)
		c := exec.Command("runuser", append(args, "xdg-open", url)...)
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err == nil {
			go c.Wait()
			return nil
		}
	}
	return fmt.Errorf("no browser found")
}

// desktopAlert shows an error on the desktop. Used when Turbo Button runs without a terminal (--gui),
// where printed messages would go nowhere.
func desktopAlert(msg string) {
	var env []string
	var pre []string
	if os.Geteuid() == 0 {
		g := desktopUser()
		if g == nil || !have("runuser") {
			return
		}
		pre = append([]string{"runuser", "-u", g.name, "--", "env", "HOME=" + homeOf(g.name)}, g.env...)
	}
	try := func(args ...string) bool {
		full := append(append([]string{}, pre...), args...)
		c := exec.Command(full[0], full[1:]...)
		c.Env = append(os.Environ(), env...)
		return c.Run() == nil
	}
	switch {
	case have("kdialog") && try("kdialog", "--title", "Turbo Button", "--error", msg):
	case have("zenity") && try("zenity", "--error", "--title=Turbo Button", "--text="+msg):
	case have("notify-send"):
		try("notify-send", "-u", "critical", "Turbo Button", msg)
	}
}

func closeScreen(workDir string) {
	if mockMode() {
		return
	}
	prof := filepath.Join(os.TempDir(), "turbobutton-screen-"+filepath.Base(workDir))
	exec.Command("pkill", "-f", "--", "--user-data-dir="+prof).Run()
	time.Sleep(300 * time.Millisecond)
	os.RemoveAll(prof)
}

func homeOf(name string) string {
	if u, err := user.Lookup(name); err == nil {
		return u.HomeDir
	}
	return "/home/" + name
}

// ---- firewall: open the port only while the session runs ----

var fwUndo []string

func addFirewallRule(port int) {
	if mockMode() {
		return
	}
	p := strconv.Itoa(port)
	if have("ufw") {
		if out, _ := exec.Command("ufw", "status").Output(); strings.Contains(string(out), "Status: active") {
			if exec.Command("ufw", "allow", p+"/tcp", "comment", "Turbo Button session").Run() == nil {
				fwUndo = append(fwUndo, "ufw delete allow "+p+"/tcp")
			}
		}
	}
	if have("firewall-cmd") && exec.Command("firewall-cmd", "--state").Run() == nil {
		if exec.Command("firewall-cmd", "--add-port="+p+"/tcp").Run() == nil { // runtime only: gone after reboot anyway
			fwUndo = append(fwUndo, "firewall-cmd --remove-port="+p+"/tcp")
		}
	}
}

func removeFirewallRule() {
	for _, c := range fwUndo {
		f := strings.Fields(c)
		exec.Command(f[0], f[1:]...).Run()
	}
	fwUndo = nil
}

// keepAwake blocks sleep and idle suspend while the agent runs.
func keepAwake() func() {
	if mockMode() || !have("systemd-inhibit") {
		return func() {}
	}
	c := exec.Command("systemd-inhibit", "--what=sleep:idle:handle-lid-switch", "--who=Turbo Button", "--why=Servicing this PC", "--mode=block", "sleep", "infinity")
	if c.Start() != nil {
		return func() {}
	}
	return func() {
		if c.Process != nil {
			c.Process.Kill()
			c.Wait()
		}
	}
}

// ---- resume after restart: not on Linux yet ----

func resumeDir() string {
	if d := os.Getenv("TURBO_RESUME_DIR"); d != "" {
		return d
	}
	return "/var/lib/turbobutton"
}
func lockDown(p string) { os.Chmod(p, 0600) }
func scheduleResume(string) error {
	if mockMode() {
		return nil
	}
	return fmt.Errorf("auto-resume after restart isn't available on Linux yet")
}
func unscheduleResume()             {}
func deleteDirAfterExit(dir string) { os.RemoveAll(dir) }

func machineID() string {
	if id := os.Getenv("TURBO_MOCK_ID"); id != "" {
		return id
	}
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	if mockMode() {
		return "mock-machine-1"
	}
	return ""
}

// readKV parses KEY=value files like /etc/os-release.
func readKV(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return m
}
