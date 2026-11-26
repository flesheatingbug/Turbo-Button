package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Param describes one parameter of an action. Values are passed to the
// PowerShell script through environment variables (PCM_<NAME>), never
// spliced into script text, so they cannot inject commands.
type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // enum | string | bool
	Enum    []string `json:"enum,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Desc    string   `json:"desc"`
	Opt     bool     `json:"optional,omitempty"` // may be left out (bool → false, string → blank)
}

type Action struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Category   string  `json:"category"`
	Risk       string  `json:"risk"` // low | medium | high
	Desc       string  `json:"desc"`
	Params     []Param `json:"params"`
	Long       bool    `json:"long"`             // takes minutes
	Modifies   bool    `json:"modifies"`         // changes the system (restore point first)
	TimeoutMin int     `json:"-"`                // hard time limit (default 30)
	Reboot     bool    `json:"reboot,omitempty"` // needs or causes a restart
}

var reNoCtrl = `^[^\x00-\x1f]{1,260}$`

var Catalog = []Action{
	{ID: "restore_point", Title: "Create a System Restore point", Category: "safety", Risk: "low",
		Desc: "Turns on System Protection for the system drive if needed and creates a restore point. Created automatically before the first change unless switched off."},

	{ID: "cleanup", Title: "Clean up files", Category: "cleanup", Risk: "low",
		Desc:     "Deletes regenerable files for one target and reports space freed. recycle_bin deletes the customer's deleted files permanently (high risk).",
		Modifies: true,
		Params: []Param{{Name: "target", Type: "enum", Desc: "What to clean",
			Enum: []string{"user_temp", "windows_temp", "update_cache", "delivery_optimization", "crash_dumps", "browser_cache", "thumbnail_cache", "recycle_bin"}}}},

	{ID: "component_cleanup", TimeoutMin: 45, Title: "Clean up the Windows component store (DISM)", Category: "cleanup", Risk: "low", Long: true, Modifies: true,
		Desc: "DISM /StartComponentCleanup: removes superseded update components. Takes 5-20 minutes."},

	{ID: "startup_disable", Title: "Disable a startup app", Category: "startup", Risk: "low", Modifies: true,
		Desc: "Disables a startup entry the same way Task Manager does (reversible with startup_enable). Use location and name exactly as in the scan.",
		Params: []Param{
			{Name: "location", Type: "enum", Desc: "Where the entry lives (from scan)", Enum: []string{"HKCU_Run", "HKLM_Run", "HKLM_Run32", "Startup_User", "Startup_Common"}},
			{Name: "name", Type: "string", Pattern: reNoCtrl, Desc: "Entry name from scan"}}},

	{ID: "startup_enable", Title: "Re-enable a startup app", Category: "startup", Risk: "low", Modifies: true,
		Desc: "Undo for startup_disable.",
		Params: []Param{
			{Name: "location", Type: "enum", Desc: "Where the entry lives", Enum: []string{"HKCU_Run", "HKLM_Run", "HKLM_Run32", "Startup_User", "Startup_Common"}},
			{Name: "name", Type: "string", Pattern: reNoCtrl, Desc: "Entry name"}}},

	{ID: "task_disable", Title: "Disable a scheduled task", Category: "startup", Risk: "medium", Modifies: true,
		Desc: "Disables a non-Microsoft scheduled task (updaters, telemetry from OEM tools, etc.).",
		Params: []Param{
			{Name: "path", Type: "string", Pattern: `^\\[^\x00-\x1f]{0,250}$`, Desc: "TaskPath from scan, e.g. \\ or \\Vendor\\"},
			{Name: "name", Type: "string", Pattern: `^[^\x00-\x1f\\]{1,250}$`, Desc: "TaskName from scan"}}},

	{ID: "service_mode", Title: "Change a service's startup type", Category: "services", Risk: "medium", Modifies: true,
		Desc: "Sets a service to Manual, Disabled or Automatic (stops it when Disabled). Core and security services are refused.",
		Params: []Param{
			{Name: "name", Type: "string", Pattern: `^[A-Za-z0-9_.\- $]{1,80}$`, Desc: "Service name (not display name)"},
			{Name: "mode", Type: "enum", Enum: []string{"Manual", "Disabled", "Automatic"}, Desc: "New startup type"}}},

	{ID: "app_remove", Title: "Remove a built-in / Store app", Category: "bloat", Risk: "medium", Modifies: true,
		Desc:   "Removes an Appx package for all users and its provisioned copy (so new users don't get it). Core apps are refused.",
		Params: []Param{{Name: "name", Type: "string", Pattern: `^[A-Za-z0-9.\-_]{2,128}$`, Desc: "Package Name from scan, e.g. Microsoft.BingNews"}}},

	{ID: "power_plan", Title: "Set power plan", Category: "performance", Risk: "low", Modifies: true,
		Desc:   "Switches the active power plan. Prefer balanced on laptops.",
		Params: []Param{{Name: "plan", Type: "enum", Enum: []string{"balanced", "high_performance"}, Desc: "Plan"}}},

	{ID: "fast_startup", Title: "Turn Fast Startup on or off", Category: "performance", Risk: "low", Modifies: true,
		Desc:   "Fast Startup can cause driver/update problems and stops 'shut down' fully resetting the PC.",
		Params: []Param{{Name: "enabled", Type: "bool", Desc: "true to enable"}}},

	{ID: "hibernation", Title: "Turn hibernation on or off", Category: "performance", Risk: "medium", Modifies: true,
		Desc:   "Off deletes hiberfil.sys (frees space roughly 40% of RAM) but also disables Fast Startup and hibernate. Avoid on laptops.",
		Params: []Param{{Name: "enabled", Type: "bool", Desc: "true to enable"}}},

	{ID: "privacy", Title: "Apply a privacy / anti-ads tweak", Category: "privacy", Risk: "low", Modifies: true,
		Desc:   "advertising_id, start_suggestions (Start/Settings ads and silent app installs), tailored_experiences, telemetry_minimum (Required diagnostic data), bing_search (web results in Start search), lockscreen_tips. Applies to the signed-in admin user plus machine policy where relevant.",
		Params: []Param{{Name: "tweak", Type: "enum", Enum: []string{"advertising_id", "start_suggestions", "tailored_experiences", "telemetry_minimum", "bing_search", "lockscreen_tips"}, Desc: "Tweak"}}},

	{ID: "optimize_drive", TimeoutMin: 60, Title: "Optimize a drive (TRIM or defrag)", Category: "disk", Risk: "low", Long: true, Modifies: true,
		Desc:   "Runs Windows' Optimize-Volume: TRIM on SSDs, defrag on hard drives.",
		Params: []Param{{Name: "drive", Type: "string", Pattern: `^[A-Za-z]$`, Desc: "Drive letter"}}},

	{ID: "chkdsk_scan", TimeoutMin: 60, Title: "Check a drive for file system errors (online)", Category: "health", Risk: "low", Long: true,
		Desc:   "chkdsk /scan: read-only online check, no reboot.",
		Params: []Param{{Name: "drive", Type: "string", Pattern: `^[A-Za-z]$`, Desc: "Drive letter"}}},

	{ID: "sfc", TimeoutMin: 60, Title: "Repair Windows system files (SFC)", Category: "health", Risk: "low", Long: true, Modifies: true,
		Desc: "sfc /scannow. 5-20 minutes. Run dism_restore first if SFC can't repair."},

	{ID: "dism_restore", TimeoutMin: 90, Title: "Repair the Windows image (DISM RestoreHealth)", Category: "health", Risk: "low", Long: true, Modifies: true,
		Desc: "DISM /RestoreHealth, downloads clean files from Windows Update. 10-30 minutes, needs internet."},

	{ID: "defender_update", Title: "Update Microsoft Defender signatures", Category: "security", Risk: "low", Modifies: true,
		Desc: "Pulls the latest malware definitions."},

	{ID: "defender_scan", TimeoutMin: 240, Title: "Run a Microsoft Defender scan", Category: "security", Risk: "low", Long: true,
		Desc:   "Quick scan takes a few minutes; full scan can take an hour or more. Reports any detections.",
		Params: []Param{{Name: "type", Type: "enum", Enum: []string{"quick", "full"}, Desc: "Scan type"}}},

	{ID: "defender_remove", Title: "Remove threats Defender found", Category: "security", Risk: "medium", Modifies: true, TimeoutMin: 30,
		Desc: "Uses Defender's own clean-up to remove threats it detected that are still on the PC (not quarantined yet). Threats the customer deliberately allowed in Windows Security are left alone and listed. If something survives, follow with defender_offline_scan."},

	{ID: "dns_flush", Title: "Flush DNS cache", Category: "network", Risk: "low", Modifies: true,
		Desc: "ipconfig /flushdns."},

	// ---- Updates ----
	{ID: "windows_update", Title: "Windows Update", Category: "updates", Risk: "low", Long: true, Modifies: true, TimeoutMin: 240, Reboot: true,
		Desc: "mode=check lists pending updates; mode=install downloads and installs them (can take 10-60+ min, usually needs a restart). drivers=true also installs driver updates from Windows Update.",
		Params: []Param{
			{Name: "mode", Type: "enum", Enum: []string{"check", "install"}, Desc: "check or install"},
			{Name: "drivers", Type: "bool", Desc: "include driver updates"}}},

	{ID: "problem_check", Title: "Look into the reported problem", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 15,
		Desc:   "Read-only. terms = keywords from the customer's problem (e.g. \"onedrive sign login\"). Collects matching warnings/errors from the last 7 days (incl. AAD/OneDrive/print/Wi-Fi logs when relevant), matching processes, services and programs, and for sign-in problems the account join state (dsregcmd), clock, proxy and whether Microsoft's sign-in servers are reachable.",
		Params: []Param{{Name: "terms", Type: "string", Pattern: `^[a-z0-9 ._+-]{3,200}$`, Desc: "space-separated keywords"}}},

	{ID: "driver_updates", Title: "Update drivers", Category: "updates", Risk: "low", Long: true, Modifies: true, TimeoutMin: 120, Reboot: true,
		Desc: "mode=check lists newer drivers; mode=install installs driver updates from Windows Update, then runs the PC maker's own updater (Dell Command Update, Lenovo System Update, HP Image Assistant) if it's installed. oemtool=true installs Dell/Lenovo's updater with winget first if missing. Also reports graphics drivers that are still old.",
		Params: []Param{
			{Name: "mode", Type: "enum", Enum: []string{"check", "install"}, Desc: "check or install"},
			{Name: "oemtool", Type: "bool", Opt: true, Desc: "install the maker's updater if missing"}}},

	{ID: "feature_update", Title: "Upgrade to the newest Windows 11 version", Category: "updates", Risk: "medium", Long: true, Modifies: true, TimeoutMin: 240, Reboot: true,
		Desc: "mode=check compares this PC's Windows version (e.g. 25H2) with the newest yearly release (read-only). mode=install briefly pins Windows Update to that version so it offers it (a quick enablement package on 24H2/25H2), installs it and removes the pin; needs a restart. assistant=true falls back to Microsoft's Windows 11 Installation Assistant (full in-place upgrade, keeps files and programs, 1h+) if Windows Update won't offer it. bypass=true (technician's call, for a PC that fails Microsoft's TPM 2.0 / Secure Boot / CPU requirements) sets the documented override keys and, with iso set to a Windows 11 ISO path, upgrades from it with the hardware check skipped (keeps files and programs); without an ISO it relies on the Installation Assistant, which only covers TPM 1.2 / unsupported-CPU PCs. Leaves PCs managed by WSUS/Intune alone and never overrides Microsoft's compatibility holds.",
		Params: []Param{
			{Name: "mode", Type: "enum", Enum: []string{"check", "install"}, Desc: "check or install"},
			{Name: "version", Type: "string", Opt: true, Pattern: `^(\d\dH[12])?$`, Desc: "target version like 26H2 (blank = newest)"},
			{Name: "assistant", Type: "bool", Opt: true, Desc: "fall back to the Installation Assistant"},
			{Name: "bypass", Type: "bool", Opt: true, Desc: "skip Microsoft's hardware requirements (unsupported PC)"},
			{Name: "iso", Type: "string", Opt: true, Pattern: `^(|[A-Za-z]:\\[^\r\n"|<>]{1,250}\.[Ii][Ss][Oo])$`, Desc: "path to a Windows 11 ISO for the bypass upgrade"}}},

	{ID: "app_updates", Title: "Update installed programs (winget)", Category: "updates", Risk: "low", Long: true, TimeoutMin: 120,
		Desc:   "mode=list shows programs with updates; mode=install silently updates all of them with Windows Package Manager. Needs App Installer (winget).",
		Params: []Param{{Name: "mode", Type: "enum", Enum: []string{"list", "install"}, Desc: "list or install"}}},

	{ID: "restart_pc", Title: "Restart the PC", Category: "updates", Risk: "medium", Reboot: true,
		Desc:   "Restarts after a countdown shown to the user. Ends the session (phone disconnects) - always the LAST action.",
		Params: []Param{{Name: "minutes", Type: "enum", Enum: []string{"1", "5"}, Desc: "countdown"}}},

	{ID: "cancel_restart", Title: "Cancel a pending restart", Category: "updates", Risk: "low",
		Desc: "Cancels a restart started by restart_pc."},

	// ---- Junk & adware ----
	{ID: "program_uninstall", Title: "Uninstall a program", Category: "junk", Risk: "medium", Long: true, Modifies: true, TimeoutMin: 30,
		Desc:   "Silently uninstalls a desktop program by its exact name from the scan (quiet uninstaller, MSI, or winget). Programs without a silent uninstaller must be removed by hand. Drivers, runtimes, Office and security software are refused.",
		Params: []Param{{Name: "name", Type: "string", Pattern: reNoCtrl, Desc: "Exact program name from scan.programs"}}},

	{ID: "office_remove", Title: "Remove Microsoft Office", Category: "junk", Risk: "high", Long: true, Modifies: true, TimeoutMin: 45, Reboot: true,
		Desc:   "Silently removes one Microsoft Office install by its exact name from scan.office: any version (2007-2016 MSI, 2013+/2019/2021/2024, Microsoft 365 Click-to-Run). Closes Office apps first. Outlook data files are kept. The customer needs their Microsoft account or product key to reinstall, so always ask the customer. Use for broken installs before a reinstall, expired trials, or duplicate Office versions.",
		Params: []Param{{Name: "name", Type: "string", Pattern: reNoCtrl, Desc: "Exact name from scan.office"}}},
	{ID: "av_remove", Title: "Remove an antivirus", Category: "junk", Risk: "high", Long: true, Modifies: true, TimeoutMin: 40, Reboot: true,
		Desc:   "Removes one third-party antivirus/security suite by its exact name from scan.securitySoftware (McAfee, Norton, Avast, AVG, Kaspersky, ESET, Bitdefender…). Uses the silent uninstaller when there is one; otherwise the vendor's uninstaller opens on the PC and the technician clicks through it there. Microsoft Defender takes over after a restart. Use for expired trials, preinstalled trialware, or two antiviruses fighting. Always ask the customer. Never removes Microsoft Defender.",
		Params: []Param{{Name: "name", Type: "string", Pattern: reNoCtrl, Desc: "Exact name from scan.securitySoftware"}}},

	{ID: "reset_proxy", Title: "Remove proxy settings", Category: "junk", Risk: "medium", Modifies: true,
		Desc: "Turns off the user's proxy / auto-config script and resets the system (WinHTTP) proxy. Adware often sets these. Business PCs may need their proxy."},

	{ID: "reset_dns", Title: "Reset DNS servers to automatic", Category: "junk", Risk: "medium", Modifies: true,
		Desc: "Sets every connected network adapter back to automatic (DHCP) DNS. Use when DNS was hijacked. Business PCs may need fixed DNS."},

	{ID: "reset_hosts", Title: "Reset the hosts file", Category: "junk", Risk: "medium", Modifies: true,
		Desc: "Backs up the hosts file to C:\\ProgramData\\TurboButton\\backups and restores the Windows default (removes redirects/blocks added by malware or tools)."},

	{ID: "remove_browser_policies", Title: "Remove forced browser policies", Category: "junk", Risk: "high", Modifies: true,
		Desc: "Backs up and deletes Chrome/Edge/Brave policy keys (forced extensions, homepage, search engine) - a common adware trick. Never on business/managed PCs."},

	// ---- Repair toolkit ----
	{ID: "reset_windows_update", Title: "Reset Windows Update components", Category: "repair", Risk: "medium", Modifies: true, TimeoutMin: 15,
		Desc: "Stops update services, renames SoftwareDistribution and catroot2 (kept as .old), restarts services. Fixes stuck or failing Windows Update."},

	{ID: "network_reset", Title: "Reset network stack", Category: "repair", Risk: "medium", Modifies: true, Reboot: true,
		Desc: "netsh winsock reset + TCP/IP reset + DNS flush. Fixes broken internet after malware/VPN removal. Needs a restart; custom static IPs are kept but VPN adapters may need reinstalling."},

	{ID: "store_repair", Title: "Repair Microsoft Store", Category: "repair", Risk: "low", Modifies: true, TimeoutMin: 15,
		Desc: "Re-registers the Microsoft Store app for the signed-in admin user."},

	{ID: "search_rebuild", Title: "Rebuild Windows Search index", Category: "repair", Risk: "low", Modifies: true,
		Desc: "Resets the search index; it rebuilds in the background. Fixes Start/Explorer search not finding files."},

	{ID: "schedule_chkdsk", Title: "Schedule a full disk check at restart", Category: "repair", Risk: "medium", Modifies: true, Reboot: true,
		Desc:   "Marks the drive so Windows runs a full CHKDSK repair at the next restart (can take a long time on big drives).",
		Params: []Param{{Name: "drive", Type: "string", Pattern: `^[A-Za-z]$`, Desc: "Drive letter"}}},

	{ID: "schedule_memory_test", Title: "Schedule a memory (RAM) test at restart", Category: "repair", Risk: "low", Modifies: true, Reboot: true,
		Desc: "Windows Memory Diagnostic runs once at the next restart (10-20 min). Results show in the next scan."},

	{ID: "defender_offline_scan", Title: "Microsoft Defender offline scan (restarts NOW)", Category: "security", Risk: "high", Reboot: true,
		Desc: "Restarts immediately into a deep malware scan (~15 min) for rootkits. Ends the session."},

	// ---- Diagnostics (read-only, used by "Deep checks") ----
	{ID: "sfc_verify", Title: "Verify Windows system files (read-only)", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 60,
		Desc: "sfc /verifyonly: checks system files for corruption without changing anything (5-20 min)."},

	{ID: "dism_check", Title: "Check the Windows image for corruption (read-only)", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 60,
		Desc: "DISM /ScanHealth: checks the component store for corruption without repairing (5-15 min)."},

	{ID: "disk_usage", Title: "Find what's using disk space", Category: "diagnostics", Risk: "low", Long: true, TimeoutMin: 30,
		Desc: "Measures user folders (Desktop, Documents, Downloads, Pictures, Videos, AppData...) and big system folders. Read-only."},
}

// Things that are never touched, whatever is asked.
var protectedServices = map[string]bool{}
var protectedAppPrefixes = []string{
	"Microsoft.WindowsStore", "Microsoft.StorePurchaseApp", "Microsoft.DesktopAppInstaller", "Microsoft.SecHealthUI",
	"Microsoft.VCLibs", "Microsoft.NET.", "Microsoft.UI.Xaml", "Microsoft.WindowsAppRuntime", "Microsoft.MicrosoftEdge",
	"Microsoft.Windows.", "Microsoft.AAD.", "Microsoft.AccountsControl", "Microsoft.LockApp", "Microsoft.Win32WebViewHost",
	"windows.", "Microsoft.WebExperience", "Microsoft.Services.Store", "Microsoft.WindowsCalculator", "Microsoft.WindowsNotepad",
	"Microsoft.Paint", "Microsoft.ScreenSketch", "Microsoft.Windows.Photos", "Microsoft.HEIFImageExtension", "Microsoft.HEVCVideoExtension",
	"Microsoft.VP9VideoExtensions", "Microsoft.WebpImageExtension", "Microsoft.RawImageExtension", "Microsoft.AV1VideoExtension",
	"MicrosoftWindows.Client.", "MicrosoftCorporationII.QuickAssist", "Microsoft.WindowsTerminal", "NcsiUwpApp",
}

var protectedProgram = regexp.MustCompile(`(?i)(visual c\+\+|\.net|directx|microsoft edge|webview|chipset|realtek|nvidia|amd software|radeon|intel\(r\)|synaptics|elan |firmware|bios|microsoft office|microsoft 365|defender|turbo button|onedrive|windows (sdk|driver|software development)|vcredist|runtime)`)

func init() {
	for _, s := range []string{
		"WinDefend", "WdNisSvc", "WdNisDrv", "WdFilter", "Sense", "SecurityHealthService", "mpssvc", "BFE", "wscsvc",
		"wuauserv", "UsoSvc", "WaaSMedicSvc", "BITS", "TrustedInstaller", "CryptSvc", "msiserver", "AppXSvc", "ClipSVC", "LicenseManager",
		"RpcSs", "RpcEptMapper", "DcomLaunch", "LSM", "SamSs", "EventLog", "Winmgmt", "Schedule", "ProfSvc", "gpsvc", "Power",
		"PlugPlay", "Dhcp", "Dnscache", "nsi", "NlaSvc", "netprofm", "Wcmsvc", "WlanSvc", "LanmanWorkstation", "AudioSrv",
		"AudioEndpointBuilder", "Themes", "UserManager", "CoreMessagingRegistrar", "StateRepository", "TimeBrokerSvc", "SystemEventsBroker",
		"BrokerInfrastructure", "EFS", "KeyIso", "VaultSvc", "Netlogon", "W32Time", "TokenBroker", "camsvc", "DeviceInstall", "DispBrokerDesktopSvc",
		"TextInputManagementService", "WinHttpAutoProxySvc", "iphlpsvc", "mpsdrv", "vmcompute", "VSS", "swprv", "SENS", "ShellHWDetection",
	} {
		protectedServices[strings.ToLower(s)] = true
	}
}

func findAction(id string) *Action {
	cat := activeCatalog()
	for i := range cat {
		if cat[i].ID == id {
			return &cat[i]
		}
	}
	return nil
}

// ValidateParams checks params against the action's schema and safety lists,
// and returns them as string values for the environment.
func ValidateParams(a *Action, in map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for k := range in {
		known := false
		for _, p := range a.Params {
			if p.Name == k {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown parameter %q", k)
		}
	}
	for _, p := range a.Params {
		raw, ok := in[p.Name]
		if !ok {
			if p.Opt {
				continue
			}
			return nil, fmt.Errorf("missing parameter %q", p.Name)
		}
		var v string
		switch p.Type {
		case "bool":
			switch t := raw.(type) {
			case bool:
				if t {
					v = "1"
				} else {
					v = "0"
				}
			case string:
				switch strings.ToLower(t) {
				case "true", "1", "yes", "on":
					v = "1"
				case "false", "0", "no", "off":
					v = "0"
				default:
					return nil, fmt.Errorf("%s must be true or false", p.Name)
				}
			default:
				return nil, fmt.Errorf("%s must be true or false", p.Name)
			}
		case "enum":
			s, _ := raw.(string)
			okv := false
			for _, e := range p.Enum {
				if e == s {
					okv = true
				}
			}
			if !okv {
				return nil, fmt.Errorf("%s must be one of %v", p.Name, p.Enum)
			}
			v = s
		default:
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be text", p.Name)
			}
			if p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(s) {
				return nil, fmt.Errorf("%s has invalid characters or length", p.Name)
			}
			v = s
		}
		out[p.Name] = v
	}
	if isLinuxAgent() {
		if err := linuxProtect(a, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	switch a.ID {
	case "service_mode":
		if protectedServices[strings.ToLower(out["name"])] {
			return nil, fmt.Errorf("service %q is protected (core or security service)", out["name"])
		}
	case "program_uninstall":
		if protectedProgram.MatchString(out["name"]) {
			return nil, fmt.Errorf("program %q is protected (driver, runtime, Office or security software)", out["name"])
		}
	case "av_remove":
		if regexp.MustCompile(`(?i)defender|microsoft|windows security|turbo button`).MatchString(out["name"]) {
			return nil, fmt.Errorf("%q can't be removed: Microsoft Defender is part of Windows", out["name"])
		}
	case "office_remove":
		if !regexp.MustCompile(`(?i)^(microsoft (office|365|word|excel|powerpoint|outlook|access|publisher|visio|project|onenote)|office 16)`).MatchString(out["name"]) {
			return nil, fmt.Errorf("%q isn't a Microsoft Office install", out["name"])
		}
	case "app_remove":
		for _, pre := range protectedAppPrefixes {
			if strings.HasPrefix(strings.ToLower(out["name"]), strings.ToLower(pre)) {
				return nil, fmt.Errorf("app %q is protected (core Windows component)", out["name"])
			}
		}
	}
	return out, nil
}
