//go:build linux

package main

// Rescue (offline) mode: boots on a live Linux (e.g. SystemRescue on a Ventoy USB) next to a
// Windows PC that won't boot or keeps blue-screening, and reads that Windows off the disk
// read-only: blue-screen bugcheck codes from the minidumps, whether the boot files and registry
// are intact (and recoverable), and drive SMART health. Nothing on the patient disk is changed.

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// bugcheckName maps common Windows stop codes to their name and a plain-language hint.
var bugcheckName = map[uint32][2]string{
	0x0000000A: {"IRQL_NOT_LESS_OR_EQUAL", "a driver touched memory it shouldn't — usually a bad/old driver"},
	0x0000001E: {"KMODE_EXCEPTION_NOT_HANDLED", "a driver crashed — often graphics or a recently updated driver"},
	0x00000050: {"PAGE_FAULT_IN_NONPAGED_AREA", "bad RAM or a faulty driver — run a memory test"},
	0x0000007E: {"SYSTEM_THREAD_EXCEPTION_NOT_HANDLED", "a driver threw an unhandled error — note the driver named on the blue screen"},
	0x0000007B: {"INACCESSIBLE_BOOT_DEVICE", "Windows can't reach the boot disk — storage driver, BIOS SATA/RAID mode changed, or disk failing"},
	0x000000D1: {"DRIVER_IRQL_NOT_LESS_OR_EQUAL", "a driver accessed bad memory — frequently network or storage drivers"},
	0x0000003B: {"SYSTEM_SERVICE_EXCEPTION", "a driver or system service faulted — often graphics or antivirus"},
	0x0000009F: {"DRIVER_POWER_STATE_FAILURE", "a driver mishandled sleep/wake — usually a power or chipset driver"},
	0x00000133: {"DPC_WATCHDOG_VIOLATION", "a driver (often storage/SSD firmware) stalled — update storage drivers/firmware"},
	0x000000EF: {"CRITICAL_PROCESS_DIED", "a key Windows process died — often corrupt system files"},
	0x00000124: {"WHEA_UNCORRECTABLE_ERROR", "a hardware fault reported by the CPU — heat, failing CPU/RAM, or power"},
	0x00000139: {"KERNEL_SECURITY_CHECK_FAILURE", "corrupt data structure — bad driver, RAM, or system files"},
	0x000000C2: {"BAD_POOL_CALLER", "a driver misused memory — usually a bad/old driver"},
	0x00000019: {"BAD_POOL_HEADER", "memory corruption — bad driver or failing RAM"},
	0x000000F4: {"CRITICAL_OBJECT_TERMINATION", "a critical process ended — often a failing disk or corrupt files"},
	0x000000BE: {"ATTEMPTED_WRITE_TO_READONLY_MEMORY", "a driver wrote to read-only memory — a bad driver"},
	0x0000001A: {"MEMORY_MANAGEMENT", "memory error — test the RAM; sometimes a driver"},
	0x00000109: {"CRITICAL_STRUCTURE_CORRUPTION", "kernel memory was corrupted — bad driver or RAM"},
	0x000000FC: {"ATTEMPTED_EXECUTE_OF_NOEXECUTE_MEMORY", "a driver executed bad memory — a bad driver"},
	0x0000004A: {"IRQL_GT_ZERO_AT_SYSTEM_SERVICE", "a driver returned at the wrong IRQL — a bad driver"},
	0x000000C4: {"DRIVER_VERIFIER_DETECTED_VIOLATION", "Driver Verifier caught a bad driver — turn it off and note the driver"},
	0x0000007A: {"KERNEL_DATA_INPAGE_ERROR", "couldn't read a page from disk — failing disk or cabling"},
	0x00000077: {"KERNEL_STACK_INPAGE_ERROR", "couldn't read from disk — failing disk or bad block"},
}

type bsod struct {
	File   string `json:"file"`
	When   string `json:"when"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Hint   string `json:"hint"`
	Param1 string `json:"param1"`
	Is64   bool   `json:"is64"`
}

// parseMinidump reads the DUMP_HEADER at the start of a Windows kernel dump and returns the
// bug-check code and first parameter. Works for the small dumps in \Windows\Minidump and MEMORY.DMP.
func parseMinidump(path string) (*bsod, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hdr := make([]byte, 0x70)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	sig := string(hdr[0:8])
	is64 := strings.HasPrefix(sig, "PAGEDU64")
	is32 := strings.HasPrefix(sig, "PAGEDUMP")
	if !is64 && !is32 {
		return nil, fmt.Errorf("not a Windows dump")
	}
	code := binary.LittleEndian.Uint32(hdr[0x38:0x3c])
	var p1 uint64
	if is64 {
		p1 = binary.LittleEndian.Uint64(hdr[0x40:0x48])
	} else {
		p1 = uint64(binary.LittleEndian.Uint32(hdr[0x3c:0x40]))
	}
	b := &bsod{File: filepath.Base(path), Code: fmt.Sprintf("0x%08X", code), Param1: fmt.Sprintf("0x%X", p1), Is64: is64}
	if nm, ok := bugcheckName[code]; ok {
		b.Name, b.Hint = nm[0], nm[1]
	} else {
		b.Name = "(uncommon stop code)"
	}
	if fi, err := f.Stat(); err == nil {
		b.When = fi.ModTime().UTC().Format("2006-01-02 15:04")
	}
	return b, nil
}

// findWindowsRoots returns directories that look like a Windows installation (case-insensitive
// Windows\System32\config\SYSTEM) under the given mount points.
func findWindowsRoots(mounts []string) []string {
	var roots []string
	for _, m := range mounts {
		win := casePath(m, "Windows", "System32", "config", "SYSTEM")
		if win != "" {
			roots = append(roots, strings.TrimSuffix(win, caseJoin("Windows", "System32", "config", "SYSTEM")))
		}
	}
	return roots
}

// casePath resolves a path case-insensitively one segment at a time (Windows dirs vary in case on
// ntfs-3g). Returns the real path if every segment is found, else "".
func casePath(base string, segs ...string) string {
	cur := base
	for _, want := range segs {
		ents, err := os.ReadDir(cur)
		if err != nil {
			return ""
		}
		found := ""
		for _, e := range ents {
			if strings.EqualFold(e.Name(), want) {
				found = filepath.Join(cur, e.Name())
				break
			}
		}
		if found == "" {
			return ""
		}
		cur = found
	}
	return cur
}

func caseJoin(segs ...string) string { return filepath.Join(segs...) }

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return -1
}

// offlineScan examines one offline Windows installation (its root, e.g. /mnt/win).
func offlineScan(ctx context.Context, root string, onLine func(string)) map[string]any {
	res := map[string]any{"root": root}
	onLine("Reading Windows at " + root)

	// Blue screens.
	var dumps []*bsod
	if md := casePath(root, "Windows", "Minidump"); md != "" {
		ents, _ := os.ReadDir(md)
		for _, e := range ents {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".dmp") {
				if b, err := parseMinidump(filepath.Join(md, e.Name())); err == nil {
					dumps = append(dumps, b)
				}
			}
		}
	}
	if mem := casePath(root, "Windows", "MEMORY.DMP"); mem != "" {
		if b, err := parseMinidump(mem); err == nil {
			dumps = append(dumps, b)
		}
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].When > dumps[j].When })
	if len(dumps) > 15 {
		dumps = dumps[:15]
	}
	res["blueScreens"] = dumps
	if len(dumps) > 0 {
		onLine(fmt.Sprintf("%d crash dump(s); newest: %s %s", len(dumps), dumps[0].Code, dumps[0].Name))
	} else {
		onLine("No crash dumps found (blue screens may be disabled, or the crash was too early to save one).")
	}

	// Boot / registry health.
	boot := map[string]any{}
	sysHive := casePath(root, "Windows", "System32", "config", "SYSTEM")
	boot["systemHiveBytes"] = fileSize(sysHive)
	for _, h := range []string{"SYSTEM", "SOFTWARE", "SAM", "DEFAULT", "SECURITY"} {
		p := casePath(root, "Windows", "System32", "config", h)
		rb := casePath(root, "Windows", "System32", "config", "RegBack", h)
		if (p == "" || fileSize(p) == 0) && rb != "" && fileSize(rb) > 0 {
			boot["recoverableHive_"+h] = true
			onLine("Registry hive " + h + " is missing/empty but a backup exists in RegBack — recoverable.")
		}
	}
	boot["winload"] = casePath(root, "Windows", "System32", "winload.efi") != "" || casePath(root, "Windows", "System32", "winload.exe") != ""
	boot["ntoskrnl"] = casePath(root, "Windows", "System32", "ntoskrnl.exe") != ""
	pend := casePath(root, "Windows", "WinSxS", "pending.xml")
	boot["pendingUpdate"] = pend != "" && fileSize(pend) > 0
	res["boot"] = boot
	if sz, _ := boot["systemHiveBytes"].(int64); sz == 0 {
		onLine("The SYSTEM registry hive is 0 bytes — a classic 'won't boot' cause; often fixable from RegBack.")
	}
	if boot["pendingUpdate"] == true {
		onLine("A Windows update was mid-install (pending.xml) — a stuck update can block boot.")
	}

	res["recommended"] = offlineAdvice(dumps, boot)
	return res
}

func offlineAdvice(dumps []*bsod, boot map[string]any) []string {
	var a []string
	if sz, _ := boot["systemHiveBytes"].(int64); sz == 0 {
		a = append(a, "Restore the SYSTEM registry hive from Windows\\System32\\config\\RegBack, then try booting.")
	}
	if boot["pendingUpdate"] == true {
		a = append(a, "Clear the stuck pending update (DISM /Image:… /Cleanup-Image /RevertPendingActions from WinPE) if boot hangs on updates.")
	}
	if boot["winload"] == false || boot["ntoskrnl"] == false {
		a = append(a, "Core boot files are missing — repair with bootrec/bcdboot from Windows install media.")
	}
	// Group blue screens by name for a steer.
	seen := map[string]int{}
	for _, d := range dumps {
		seen[d.Name]++
	}
	for name, n := range seen {
		if h := firstHint(dumps, name); h != "" {
			a = append(a, fmt.Sprintf("%d× %s — %s.", n, name, h))
		}
	}
	return a
}

func firstHint(dumps []*bsod, name string) string {
	for _, d := range dumps {
		if d.Name == name {
			return d.Hint
		}
	}
	return ""
}

// rescueScan mounts Windows partitions read-only, scans each Windows found, and adds SMART health.
func rescueScan(ctx context.Context, onLine func(string)) map[string]any {
	onLine("Rescue mode: looking for Windows installations on the attached disks...")
	mounts := mountWindowsPartitions(ctx, onLine)
	var wins []map[string]any
	for _, root := range findWindowsRoots(mounts) {
		w := offlineScan(ctx, root, onLine)
		if accs, err := rescueAccounts(ctx, root, onLine); err == nil {
			w["accounts"] = accs
		}
		wins = append(wins, w)
	}
	if len(wins) == 0 {
		onLine("No Windows installation found. If the disk is encrypted (BitLocker) or the partition didn't mount, unlock/mount it first.")
	}
	return map[string]any{
		"mode":      "rescue",
		"windows":   wins,
		"disks":     rescueSmart(ctx, onLine),
		"scannedAt": time.Now().UTC().Format(time.RFC3339),
	}
}

// mountWindowsPartitions finds NTFS partitions and mounts each read-only (needs root, as on a
// rescue boot). Already-mounted ones are reused. Returns mount points.
func mountWindowsPartitions(ctx context.Context, onLine func(string)) []string {
	var mounts []string
	seen := map[string]bool{}
	// Already-mounted ntfs/fuseblk first.
	for _, l := range strings.Split(readStr("/proc/mounts"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && (strings.Contains(f[2], "ntfs") || f[2] == "fuseblk") && !seen[f[1]] {
			seen[f[1]] = true
			mounts = append(mounts, unescapeMount(f[1]))
		}
	}
	// Mount the rest, read-only.
	out := out(ctx, 15*time.Second, "lsblk", "-rno", "NAME,FSTYPE,MOUNTPOINT")
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.EqualFold(f[1], "ntfs") {
			if len(f) >= 3 && f[2] != "" {
				continue // already mounted, handled above
			}
			dev := "/dev/" + f[0]
			mp := "/mnt/turbo-" + f[0]
			os.MkdirAll(mp, 0755)
			if err := runQuiet(ctx, "mount", "-o", "ro", dev, mp); err != nil {
				if have("ntfs-3g") {
					runQuiet(ctx, "ntfs-3g", "-o", "ro", dev, mp)
				}
			}
			if isMounted(mp) && !seen[mp] {
				seen[mp] = true
				mounts = append(mounts, mp)
				onLine("Mounted " + dev + " read-only.")
			}
		}
	}
	return mounts
}

func rescueSmart(ctx context.Context, onLine func(string)) []map[string]any {
	if !have("smartctl") {
		return nil
	}
	var disks []map[string]any
	for _, l := range strings.Split(out(ctx, 15*time.Second, "lsblk", "-rndo", "NAME,TYPE"), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[1] == "disk" {
			dev := "/dev/" + f[0]
			h := out(ctx, 20*time.Second, "smartctl", "-H", dev)
			status := "unknown"
			if strings.Contains(h, "PASSED") {
				status = "passed"
			} else if strings.Contains(strings.ToUpper(h), "FAILED") {
				status = "FAILING"
				onLine("SMART says " + dev + " is FAILING — back up the data before anything else.")
			}
			disks = append(disks, map[string]any{"device": dev, "smart": status})
		}
	}
	return disks
}

func runQuiet(ctx context.Context, name string, args ...string) error {
	_ = out(ctx, 20*time.Second, name, args...)
	return nil // out() swallows errors; isMounted() is the real check
}

func isMounted(mp string) bool {
	for _, l := range strings.Split(readStr("/proc/mounts"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && unescapeMount(f[1]) == mp {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	s = strings.ReplaceAll(s, `\040`, " ")
	s = strings.ReplaceAll(s, `\011`, "\t")
	return s
}

// ---- Offline account & password tools (rescue mode) ----

// rescueAccounts lists local Windows accounts from the offline SAM hive (read-only) via chntpw.
func rescueAccounts(ctx context.Context, root string, onLine func(string)) ([]map[string]any, error) {
	sam := casePath(root, "Windows", "System32", "config", "SAM")
	if sam == "" {
		return nil, fmt.Errorf("no SAM hive at %s", root)
	}
	if !have("chntpw") {
		onLine("chntpw isn't installed on this rescue system, so accounts can't be listed. (SystemRescue has it.)")
		return nil, fmt.Errorf("chntpw not available")
	}
	var accs []map[string]any
	for _, l := range strings.Split(out(ctx, 20*time.Second, "chntpw", "-l", sam), "\n") {
		// chntpw -l prints rows like: | 03e9 | Alice           | ADMIN  | dis/lock |
		if !strings.HasPrefix(strings.TrimSpace(l), "|") {
			continue
		}
		cols := strings.Split(l, "|")
		if len(cols) < 4 {
			continue
		}
		rid := strings.TrimSpace(cols[1])
		name := strings.TrimSpace(cols[2])
		if rid == "RID" || rid == "" || name == "" || strings.Contains(rid, "----") {
			continue
		}
		flags := strings.TrimSpace(strings.Join(cols[3:], " "))
		accs = append(accs, map[string]any{"name": name, "rid": rid, "admin": strings.Contains(flags, "ADMIN"), "disabled": strings.Contains(strings.ToLower(flags), "dis"), "locked": strings.Contains(strings.ToLower(flags), "lock")})
	}
	onLine(fmt.Sprintf("%d local Windows account(s).", len(accs)))
	return accs, nil
}

// rescueClearWinPassword blanks a local Windows account's password offline (chntpw). It cannot
// show the old password (passwords are hashed) - it only clears it. Does not defeat BitLocker.
func rescueClearWinPassword(ctx context.Context, root, user string, onLine func(string)) (map[string]any, error) {
	sam := casePath(root, "Windows", "System32", "config", "SAM")
	if sam == "" {
		return nil, fmt.Errorf("no SAM hive at %s", root)
	}
	if !have("chntpw") {
		return nil, fmt.Errorf("chntpw isn't installed on this rescue system")
	}
	if user == "" {
		return nil, fmt.Errorf("no account name given")
	}
	onLine("Clearing the password for " + user + " (offline)...")
	// chntpw -u user: menu 1 = clear password, then quit + save.
	err := streamStdin(ctx, onLine, "1\nq\ny\n", "chntpw", "-u", user, sam)
	if err != nil {
		return nil, err
	}
	onLine("Done. " + user + " can now sign in with a blank password (set a new one in Windows afterwards).")
	return map[string]any{"user": user, "cleared": true, "note": "Password blanked. This is a local account only; a Microsoft-account login can't be reset this way."}, nil
}

// rescueRestoreRegistry copies intact hives from Windows\System32\config\RegBack over the live
// config hives - the standard fix for a 0-byte/corrupt SYSTEM hive stopping boot. Backs up first.
func rescueRestoreRegistry(ctx context.Context, root string, onLine func(string)) (map[string]any, error) {
	cfg := casePath(root, "Windows", "System32", "config")
	if cfg == "" {
		return nil, fmt.Errorf("no config folder at %s", root)
	}
	rb := casePath(root, "Windows", "System32", "config", "RegBack")
	if rb == "" {
		return nil, fmt.Errorf("no RegBack folder - nothing to restore from")
	}
	restored := []string{}
	stamp := time.Now().Format("20060102-150405")
	for _, h := range []string{"SYSTEM", "SOFTWARE", "SAM", "SECURITY", "DEFAULT"} {
		src := filepath.Join(rb, h)
		dst := filepath.Join(cfg, h)
		if fi, err := os.Stat(src); err != nil || fi.Size() == 0 {
			continue
		}
		if cur := casePath(root, "Windows", "System32", "config", h); cur != "" {
			dst = cur
		}
		if err := copyFileLocal(dst, dst+"."+stamp+".bak"); err != nil {
			onLine("Couldn't back up " + h + ": " + err.Error())
		}
		if err := copyFileLocal(src, dst); err != nil {
			onLine("Couldn't restore " + h + ": " + err.Error())
			continue
		}
		restored = append(restored, h)
		onLine("Restored " + h + " from RegBack (old one kept as ." + stamp + ".bak).")
	}
	if len(restored) == 0 {
		return nil, fmt.Errorf("RegBack had no usable hives (they're often 0 bytes on Win10/11 by default)")
	}
	return map[string]any{"restored": restored, "note": "Restored registry hives from RegBack. Try booting Windows now."}, nil
}

// rescueClearLinuxPassword blanks root's password in an offline Linux install's /etc/shadow.
func rescueClearLinuxPassword(ctx context.Context, root, user string, onLine func(string)) (map[string]any, error) {
	if user == "" {
		user = "root"
	}
	shadow := filepath.Join(root, "etc", "shadow")
	b, err := os.ReadFile(shadow)
	if err != nil {
		return nil, fmt.Errorf("no %s (is this a Linux root?): %v", shadow, err)
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for i, l := range lines {
		f := strings.Split(l, ":")
		if len(f) > 1 && f[0] == user {
			f[1] = "" // empty = no password
			lines[i] = strings.Join(f, ":")
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("user %s not found in shadow", user)
	}
	copyFileLocal(shadow, shadow+".bak")
	if err := os.WriteFile(shadow, []byte(strings.Join(lines, "\n")), 0640); err != nil {
		return nil, err
	}
	onLine(user + "'s password cleared in " + shadow + " (original saved as shadow.bak).")
	return map[string]any{"user": user, "cleared": true}, nil
}

func copyFileLocal(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0600)
}
