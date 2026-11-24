//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

const fwRule = "Turbo Button temporary session"

// setupConsole switches the console to UTF-8 and turns on colour (VT) output; returns whether colour works.
func setupConsole() bool {
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	t, _ := syscall.UTF16PtrFromString("Turbo Button")
	kernel32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(t)))
	h, _, _ := kernel32.NewProc("GetStdHandle").Call(uintptr(0xFFFFFFF5)) // STD_OUTPUT_HANDLE (-11)
	var mode uint32
	if r, _, _ := kernel32.NewProc("GetConsoleMode").Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	r, _, _ := kernel32.NewProc("SetConsoleMode").Call(h, uintptr(mode|0x0004)) // ENABLE_VIRTUAL_TERMINAL_PROCESSING
	return r != 0
}

func isAdmin() bool {
	r, _, _ := shell32.NewProc("IsUserAnAdmin").Call()
	return r != 0
}

func relaunchAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	args, _ := syscall.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	dir, _ := syscall.UTF16PtrFromString(cwd)
	r, _, _ := shell32.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(dir)), 1)
	if r <= 32 {
		return fmt.Errorf("the administrator prompt was declined or failed (code %d)", r)
	}
	return nil
}

func powershellPath() string {
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe"
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

func netsh(args ...string) {
	c := exec.Command("netsh", args...)
	hideWindow(c)
	c.Run()
}

func addFirewallRule(port int) {
	removeFirewallRule()
	exe, _ := os.Executable()
	netsh("advfirewall", "firewall", "add", "rule", "name="+fwRule, "dir=in", "action=allow", "protocol=TCP",
		fmt.Sprintf("localport=%d", port), "remoteip=localsubnet", "profile=any", "program="+exe)
}

func removeFirewallRule() { netsh("advfirewall", "firewall", "delete", "rule", "name="+fwRule) }

func mockMode() bool                                                  { return false }
func mockRun(string, map[string]string, func(string)) ([]byte, error) { return nil, nil }

// keepAwake stops Windows sleeping, and the screen turning off or locking, while the agent runs,
// so long jobs finish and the customer screen stays visible. Back to normal when the session ends.
// Returns a func that restores normal behaviour.
func keepAwake() func() {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread() // execution state is per thread: keep it on one
		set := kernel32.NewProc("SetThreadExecutionState")
		set.Call(uintptr(0x80000000 | 0x00000001 | 0x00000002)) // ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED
		<-done
		set.Call(uintptr(0x80000000)) // ES_CONTINUOUS: back to normal
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

const resumeTask = "TurboButtonResume"

func resumeDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "TurboButton")
}

// lockDown limits a file to Administrators and SYSTEM (well-known SIDs, so it works on any Windows language).
func lockDown(path string) {
	c := exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*S-1-5-32-544:F", "*S-1-5-18:F")
	hideWindow(c)
	c.Run()
}

// scheduleResume creates a one-time task that reopens the agent (elevated) at the next logon.
func scheduleResume(exe string) error {
	c := exec.Command("schtasks", "/Create", "/TN", resumeTask, "/TR", `"`+exe+`" --resume`, "/SC", "ONLOGON", "/RL", "HIGHEST", "/F")
	hideWindow(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func unscheduleResume() {
	c := exec.Command("schtasks", "/Delete", "/TN", resumeTask, "/F")
	hideWindow(c)
	c.Run()
}

// deleteDirAfterExit removes a folder we are running from, a few seconds after we exit.
func deleteDirAfterExit(dir string) {
	c := exec.Command("cmd", "/c", "ping 127.0.0.1 -n 4 >nul & rmdir /s /q \""+dir+"\"")
	hideWindow(c)
	c.Start()
}

// machineID is Windows' per-install machine GUID: lets the phone recognise the same PC later.
func machineID() string {
	c := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid")
	hideWindow(c)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[len(f)-1])
}

// openScreen shows the customer screen in an app-style Edge (or Chrome) window with its own throwaway profile.
// Edge de-elevates itself on launch, so the window runs as the normal user.
func openScreen(url, workDir string) error {
	var cands []string
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		cands = append(cands, filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`), filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
	}
	for _, exe := range cands {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		prof := filepath.Join(workDir, "screen-profile")
		c := exec.Command(exe, "--app="+url, "--user-data-dir="+prof, "--window-size=1100,780", "--no-first-run",
			"--no-default-browser-check", "--disable-features=Translate,msEdgeSidebarV2,msHubApps", "--force-dark-mode")
		return c.Start()
	}
	return fmt.Errorf("no Edge or Chrome found")
}

// closeScreen closes the customer screen window (found by its private profile folder).
func closeScreen(workDir string) {
	dir := strings.ReplaceAll(filepath.Join(workDir, "screen-profile"), "'", "''")
	ps := "Get-CimInstance Win32_Process -Filter \"Name='msedge.exe' or Name='chrome.exe'\" | Where-Object { $_.CommandLine -like '*" + dir + "*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"
	c := exec.Command(powershellPath(), "-NoProfile", "-NonInteractive", "-Command", ps)
	hideWindow(c)
	c.Run()
}

// hideConsole hides the console window if it belongs to us alone (not a terminal the user started us from).
func hideConsole() bool {
	h, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if h == 0 {
		return false
	}
	var pids [4]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), 4)
	if n > 1 {
		return false
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	user32.NewProc("ShowWindow").Call(h, 0) // SW_HIDE
	return true
}

func showConsole() {
	if h, _, _ := kernel32.NewProc("GetConsoleWindow").Call(); h != 0 {
		syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(h, 5) // SW_SHOW
	}
}

// desktopAlert: on Windows the console window always shows errors.
func desktopAlert(msg string) {}
