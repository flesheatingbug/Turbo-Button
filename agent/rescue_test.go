//go:build linux

package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMinidump(t *testing.T) {
	h := make([]byte, 0x70)
	copy(h, "PAGEDU64")
	binary.LittleEndian.PutUint32(h[0x38:], 0xD1)          // DRIVER_IRQL_NOT_LESS_OR_EQUAL
	binary.LittleEndian.PutUint64(h[0x40:], 0xFFFFF8000012) // param1
	p := filepath.Join(t.TempDir(), "011224-1234-01.dmp")
	os.WriteFile(p, h, 0644)
	b, err := parseMinidump(p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Code != "0x000000D1" || b.Name != "DRIVER_IRQL_NOT_LESS_OR_EQUAL" || !b.Is64 || b.Hint == "" {
		t.Fatalf("got %+v", b)
	}
	if _, err := parseMinidump(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing file should error")
	}
	bad := filepath.Join(t.TempDir(), "x.dmp")
	os.WriteFile(bad, []byte("not a dump............................................................"), 0644)
	if _, err := parseMinidump(bad); err == nil {
		t.Fatal("non-dump should error")
	}
}

func TestFindWindowsAndOfflineScan(t *testing.T) {
	root := t.TempDir()
	// Build a fake offline Windows with mixed-case dirs.
	cfg := filepath.Join(root, "Windows", "System32", "config")
	os.MkdirAll(cfg, 0755)
	os.WriteFile(filepath.Join(cfg, "SYSTEM"), []byte{}, 0644) // 0 bytes → won't-boot signal
	os.MkdirAll(filepath.Join(cfg, "RegBack"), 0755)
	os.WriteFile(filepath.Join(cfg, "RegBack", "SYSTEM"), make([]byte, 4096), 0644) // recoverable
	os.MkdirAll(filepath.Join(root, "Windows", "System32"), 0755)
	os.WriteFile(filepath.Join(root, "Windows", "System32", "ntoskrnl.exe"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "Windows", "System32", "winload.efi"), []byte("x"), 0644)
	md := filepath.Join(root, "Windows", "Minidump")
	os.MkdirAll(md, 0755)
	h := make([]byte, 0x70)
	copy(h, "PAGEDU64")
	binary.LittleEndian.PutUint32(h[0x38:], 0x7B) // INACCESSIBLE_BOOT_DEVICE
	os.WriteFile(filepath.Join(md, "010124-01.dmp"), h, 0644)

	roots := findWindowsRoots([]string{root})
	if len(roots) != 1 {
		t.Fatalf("expected 1 windows root, got %v", roots)
	}
	res := offlineScan(nil, roots[0], func(string) {})
	boot := res["boot"].(map[string]any)
	if boot["systemHiveBytes"].(int64) != 0 || boot["recoverableHive_SYSTEM"] != true {
		t.Fatalf("boot: %+v", boot)
	}
	if len(res["blueScreens"].([]*bsod)) != 1 {
		t.Fatalf("bsod: %+v", res["blueScreens"])
	}
	adv := res["recommended"].([]string)
	if len(adv) == 0 {
		t.Fatal("expected recommendations")
	}
	t.Logf("advice: %v", adv)
}
