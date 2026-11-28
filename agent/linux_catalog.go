package main

import (
	"regexp"
	"runtime"
	"strings"
)

// isLinuxAgent: running for real on a Linux PC (mock mode keeps the Windows catalog for UI tests).
func isLinuxAgent() bool { return runtime.GOOS == "linux" && !mockMode() }

func activeCatalog() []Action {
	if isLinuxAgent() {
		return LinuxCatalog
	}
	return Catalog
}

func agentOS() string {
	if isLinuxAgent() {
		return "linux"
	}
	return "windows"
}

var reAutostart = `^(/etc/xdg/autostart|/home/[^/]+/\.config/autostart|/root/\.config/autostart)/[^/]+\.desktop$`
var reUnit = `^[A-Za-z0-9@._\-]{1,100}$`

var LinuxCatalog = []Action{
	{ID: "restore_point", Title: "Create a system snapshot (Snapper/Timeshift)", Category: "safety", Risk: "low", TimeoutMin: 30,
		Desc: "Creates a Snapper or Timeshift snapshot if either is set up. Done automatically before the first change; skipped if neither exists."},

	{ID: "cleanup", Title: "Clean up files", Category: "cleanup", Risk: "low", Modifies: true, TimeoutMin: 30,
		Desc:   "pkg_cache: old downloaded packages; journal: trims the system log to 200 MB / 4 weeks; user_cache: ~/.cache for every user (apps rebuild it); thumbnails; coredumps; tmp: /var/tmp files older than 10 days; trash: empties every user's Trash permanently (high risk).",
		Params: []Param{{Name: "target", Type: "enum", Desc: "What to clean", Enum: []string{"pkg_cache", "journal", "user_cache", "thumbnails", "coredumps", "tmp", "trash"}}}},

	{ID: "orphans_remove", Title: "Remove orphaned packages", Category: "cleanup", Risk: "medium", Modifies: true, Long: true, TimeoutMin: 30,
		Desc: "Removes packages that were installed as dependencies and are no longer needed (pacman -Qtdq / apt autoremove / dnf autoremove)."},

	{ID: "flatpak_unused", Title: "Remove unused Flatpak runtimes", Category: "cleanup", Risk: "low", Modifies: true, Long: true, TimeoutMin: 30,
		Desc: "flatpak uninstall --unused: removes runtimes no installed app needs."},

	{ID: "updates_check", Title: "Check for updates (packages, Flatpak, firmware)", Category: "updates", Risk: "low", Long: true, TimeoutMin: 20,
		Desc: "Refreshes package lists and lists pending updates. Read-only apart from refreshing the package database."},

	{ID: "system_update", Title: "Install all updates", Category: "updates", Risk: "medium", Long: true, Modifies: true, Reboot: true, TimeoutMin: 120,
		Desc: "Full system update with the distro's package manager (pacman -Syu / apt upgrade / dnf upgrade / zypper), then Flatpak and Snap. A kernel update needs a restart. On Arch-based systems check the news for manual steps first."},

	{ID: "fwupd_update", Title: "Install firmware updates (fwupd)", Category: "updates", Risk: "medium", Long: true, Modifies: true, Reboot: true, TimeoutMin: 60,
		Desc: "Installs BIOS/UEFI, SSD and peripheral firmware from LVFS via fwupdmgr. Some updates finish during the next restart."},

	{ID: "autostart_disable", Title: "Disable an autostart app", Category: "startup", Risk: "low", Modifies: true,
		Desc:   "Stops a desktop autostart entry (sets Hidden=true the standard XDG way; system entries are overridden per user). Reversible with autostart_enable. Use the path from the scan.",
		Params: []Param{{Name: "path", Type: "string", Pattern: reAutostart, Desc: "Autostart .desktop path from scan.startup"}}},

	{ID: "autostart_enable", Title: "Re-enable an autostart app", Category: "startup", Risk: "low", Modifies: true,
		Desc:   "Undo for autostart_disable.",
		Params: []Param{{Name: "path", Type: "string", Pattern: reAutostart, Desc: "Autostart .desktop path"}}},

	{ID: "service_disable", Title: "Disable a system service", Category: "services", Risk: "medium", Modifies: true,
		Desc:   "systemctl disable --now. Core, network, login, display and security services are refused.",
		Params: []Param{{Name: "name", Type: "string", Pattern: reUnit, Desc: "Unit name, e.g. cups.service"}}},

	{ID: "service_enable", Title: "Enable a system service", Category: "services", Risk: "low", Modifies: true,
		Desc:   "systemctl enable --now (undo for service_disable).",
		Params: []Param{{Name: "name", Type: "string", Pattern: reUnit, Desc: "Unit name"}}},

	{ID: "service_restart", Title: "Restart a service", Category: "services", Risk: "low", Modifies: true,
		Desc:   "systemctl restart, e.g. for a failed unit.",
		Params: []Param{{Name: "name", Type: "string", Pattern: reUnit, Desc: "Unit name"}}},

	{ID: "failed_reset", Title: "Clear failed-service markers", Category: "services", Risk: "low", Modifies: true,
		Desc: "systemctl reset-failed (after fixing or disabling failed units)."},

	{ID: "fstrim", Title: "TRIM SSDs", Category: "disk", Risk: "low", Long: true, Modifies: true, TimeoutMin: 30,
		Desc: "fstrim -av on all mounted filesystems that support it."},

	{ID: "smart_test", Title: "Run a SMART self-test", Category: "health", Risk: "low", Long: true, TimeoutMin: 10,
		Desc:   "Short drive self-test (about 2 minutes) with smartctl, then reports the result. Needs smartmontools.",
		Params: []Param{{Name: "device", Type: "string", Pattern: `^/dev/(sd[a-z]{1,2}|nvme[0-9]{1,2}n[0-9]{1,2}|hd[a-z]|vd[a-z])$`, Desc: "Disk, e.g. /dev/sda or /dev/nvme0n1"}}},

	{ID: "disk_test", Title: "Drive surface test (HDD/SSD)", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 90,
		Desc: "Read-only surface scan of the drive(s) to find unreadable sectors and slow regions - a failing HDD/SSD that SMART can miss. mode=sample spans the whole drive in a few minutes; mode=full reads every sector (can take hours). Never writes. Can be stopped.",
		Params: []Param{
			{Name: "mode", Type: "enum", Enum: []string{"sample", "full"}, Opt: true, Desc: "sample (fast) or full"},
			{Name: "device", Type: "string", Opt: true, Pattern: `^(|/dev/[a-z0-9]+|\\\\\.\\PHYSICALDRIVE[0-9]+)$`, Desc: "one drive (blank = all)"}}},

	{ID: "stress_test", Title: "Stress test (CPU)", Category: "diagnostics", Risk: "medium", Long: true, TimeoutMin: 12,
		Desc: "Read-only. Loads the CPU (target=cpu) or CPU and RAM (target=all) hard for minutes, watching temperature and clock speed to find overheating, throttling or instability. Stops early if the CPU reaches 97 C. Can be stopped at any time. Not for routine maintenance; the technician runs it on request.",
		Params: []Param{
			{Name: "minutes", Type: "enum", Enum: []string{"1", "3", "5"}, Desc: "how long"},
			{Name: "target", Type: "enum", Enum: []string{"cpu", "all"}, Opt: true, Desc: "cpu or cpu+ram"}}},

	{ID: "rescue_clear_password", Title: "Clear a Windows password (offline)", Category: "security", Risk: "high", Modifies: true, TimeoutMin: 10,
		Desc:   "Rescue mode. Blanks a LOCAL Windows account's password on the offline disk (chntpw). Cannot show the old password, only clear it; cannot unlock a Microsoft account or BitLocker. root = the Windows path from the rescue scan.",
		Params: []Param{{Name: "root", Type: "string", Pattern: `^/[^\x00]{1,250}$`, Desc: "Windows root (from the scan)"}, {Name: "user", Type: "string", Pattern: `^[^\r\n:]{1,64}$`, Desc: "account name"}}},

	{ID: "rescue_clear_linux_password", Title: "Clear a Linux password (offline)", Category: "security", Risk: "high", Modifies: true, TimeoutMin: 10,
		Desc:   "Rescue mode. Blanks an account's password in an offline Linux install's /etc/shadow (default root). root = that Linux root's mount path.",
		Params: []Param{{Name: "root", Type: "string", Pattern: `^/[^\x00]{1,250}$`, Desc: "Linux root mount"}, {Name: "user", Type: "string", Opt: true, Pattern: `^[a-z_][a-z0-9_-]{0,31}$`, Desc: "user (blank = root)"}}},

	{ID: "rescue_restore_registry", Title: "Restore registry from backup (offline)", Category: "repair", Risk: "high", Modifies: true, Reboot: true, TimeoutMin: 10,
		Desc:   "Rescue mode. Copies intact hives from Windows\\System32\\config\\RegBack over the live hives - the standard fix for a corrupt/0-byte SYSTEM hive stopping boot. Backs up the current ones first. root = the Windows path from the scan.",
		Params: []Param{{Name: "root", Type: "string", Pattern: `^/[^\x00]{1,250}$`, Desc: "Windows root (from the scan)"}}},

	{ID: "rescue_scan", Title: "Rescue scan (offline Windows)", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 20,
		Desc: "Read-only. For a Windows PC booted into this Linux rescue USB: mounts its disk read-only and reads blue-screen bug-check codes, boot-file/registry health (and what's recoverable) and drive SMART. Changes nothing on the Windows disk."},
	{ID: "problem_check", Title: "Look into the reported problem", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 15,
		Desc:   "Read-only. terms = keywords from the customer's problem (e.g. \"onedrive sign login\"). Collects matching warnings/errors from the last 7 days in the journal, matching processes, systemd units and packages, and for network/sign-in problems the clock, DNS and internet reachability.",
		Params: []Param{{Name: "terms", Type: "string", Pattern: `^[a-z0-9 ._+-]{3,200}$`, Desc: "space-separated keywords"}}},

	{ID: "disk_usage", Title: "Find what's using disk space", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 30,
		Desc: "Measures home folders, /var, /opt and other big folders. Read-only."},

	{ID: "dns_flush", Title: "Flush DNS cache", Category: "network", Risk: "low", Modifies: true,
		Desc: "resolvectl flush-caches."},

	{ID: "reset_hosts", Title: "Reset the hosts file", Category: "junk", Risk: "medium", Modifies: true,
		Desc: "Backs up /etc/hosts to /var/lib/turbobutton/backups and restores a default (localhost + this PC's name)."},

	{ID: "remove_browser_policies", Title: "Remove forced browser policies", Category: "junk", Risk: "high", Modifies: true,
		Desc: "Moves Chrome/Chromium/Brave/Edge/Vivaldi managed policy files and Firefox policies.json to a backup folder. Never on managed/business PCs."},

	{ID: "restart_pc", Title: "Restart the PC", Category: "updates", Risk: "medium", Reboot: true,
		Desc:   "Restarts after a countdown broadcast to logged-in users. Ends the session - always the LAST action.",
		Params: []Param{{Name: "minutes", Type: "enum", Enum: []string{"1", "5"}, Desc: "countdown"}}},

	{ID: "cancel_restart", Title: "Cancel a pending restart", Category: "updates", Risk: "low",
		Desc: "shutdown -c."},
}

var protectedUnit = regexp.MustCompile(`(?i)^(systemd-|dbus|polkit|networkmanager|network|wpa_supplicant|iwd|sshd?|ssh\.|gdm|sddm|lightdm|lxdm|greetd|display-manager|getty|udisks|upower|accounts-daemon|firewalld|ufw|nftables|iptables|apparmor|auditd|selinux|cron|cronie|crond|chronyd|ntpd|rtkit|plymouth|lvm2|mdmonitor|cryptsetup|zfs|btrfs|snapper|fwupd|packagekit|pipewire|wireplumber|bluetooth|modemmanager|thermald|tlp|power-profiles|tuned|acpid|kmod|getty)`)

func linuxProtect(a *Action, out map[string]string) error {
	switch a.ID {
	case "service_disable":
		n := strings.TrimSuffix(out["name"], ".service")
		if protectedUnit.MatchString(n) {
			return errProtected("service " + out["name"] + " is protected (core, login, network or security service)")
		}
	}
	return nil
}

type errProtected string

func (e errProtected) Error() string { return string(e) }
