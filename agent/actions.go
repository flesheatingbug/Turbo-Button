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
}

type Action struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Risk     string  `json:"risk"` // low | medium | high
	Desc     string  `json:"desc"`
	Params   []Param `json:"params"`
	Long     bool    `json:"long"`     // takes minutes
	Modifies bool    `json:"modifies"` // changes the system (restore point first)
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

	{ID: "component_cleanup", Title: "Clean up the Windows component store (DISM)", Category: "cleanup", Risk: "low", Long: true, Modifies: true,
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

	{ID: "optimize_drive", Title: "Optimize a drive (TRIM or defrag)", Category: "disk", Risk: "low", Long: true, Modifies: true,
		Desc:   "Runs Windows' Optimize-Volume: TRIM on SSDs, defrag on hard drives.",
		Params: []Param{{Name: "drive", Type: "string", Pattern: `^[A-Za-z]$`, Desc: "Drive letter"}}},

	{ID: "chkdsk_scan", Title: "Check a drive for file system errors (online)", Category: "health", Risk: "low", Long: true,
		Desc:   "chkdsk /scan: read-only online check, no reboot.",
		Params: []Param{{Name: "drive", Type: "string", Pattern: `^[A-Za-z]$`, Desc: "Drive letter"}}},

	{ID: "sfc", Title: "Repair Windows system files (SFC)", Category: "health", Risk: "low", Long: true, Modifies: true,
		Desc: "sfc /scannow. 5-20 minutes. Run dism_restore first if SFC can't repair."},

	{ID: "dism_restore", Title: "Repair the Windows image (DISM RestoreHealth)", Category: "health", Risk: "low", Long: true, Modifies: true,
		Desc: "DISM /RestoreHealth, downloads clean files from Windows Update. 10-30 minutes, needs internet."},

	{ID: "defender_update", Title: "Update Microsoft Defender signatures", Category: "security", Risk: "low", Modifies: true,
		Desc: "Pulls the latest malware definitions."},

	{ID: "defender_scan", Title: "Run a Microsoft Defender scan", Category: "security", Risk: "low", Long: true,
		Desc:   "Quick scan takes a few minutes; full scan can take an hour or more. Reports any detections.",
		Params: []Param{{Name: "type", Type: "enum", Enum: []string{"quick", "full"}, Desc: "Scan type"}}},

	{ID: "dns_flush", Title: "Flush DNS cache", Category: "network", Risk: "low", Modifies: true,
		Desc: "ipconfig /flushdns."},
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
	for i := range Catalog {
		if Catalog[i].ID == id {
			return &Catalog[i]
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
	switch a.ID {
	case "service_mode":
		if protectedServices[strings.ToLower(out["name"])] {
			return nil, fmt.Errorf("service %q is protected (core or security service)", out["name"])
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
