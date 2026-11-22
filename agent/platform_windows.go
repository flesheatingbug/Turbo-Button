//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

const fwRule = "Turbo Button temporary session"

func setupConsole() {
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	t, _ := syscall.UTF16PtrFromString("Turbo Button")
	kernel32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(t)))
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
