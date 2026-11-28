package main

import (
	"os"
	"testing"
)

func TestNewUpdateActions(t *testing.T) {
	for _, c := range []struct {
		id string
		p  map[string]any
		ok bool
	}{
		{"driver_updates", map[string]any{"mode": "install", "oemtool": true}, true},
		{"feature_update", map[string]any{"mode": "install", "version": "26H2", "assistant": false}, true},
		{"feature_update", map[string]any{"mode": "install", "version": "26H2; rm"}, false},
		{"feature_update", map[string]any{"mode": "check"}, true},
		{"driver_updates", map[string]any{"mode": "check"}, true},
		{"windows_update", map[string]any{"mode": "check", "drivers": true}, true},
		{"feature_update", map[string]any{"mode": "install", "assistant": false}, true},
		{"feature_update", map[string]any{"mode": "install", "bypass": true, "iso": `D:\Medicat\Win11_26H2.iso`}, true},
		{"feature_update", map[string]any{"mode": "install", "iso": `E:\en-gb.iso`, "matchlang": true}, true},
		{"feature_update", map[string]any{"mode": "install", "bypass": true, "iso": "not a path"}, false},
		{"defender_remove", map[string]any{}, true},
	} {
		var a *Action
		for i := range Catalog {
			if Catalog[i].ID == c.id {
				a = &Catalog[i]
			}
		}
		if a == nil {
			t.Fatalf("%s missing", c.id)
		}
		v, err := ValidateParams(a, c.p)
		if (err == nil) != c.ok {
			t.Fatalf("%s %v: err=%v", c.id, c.p, err)
		}
		t.Log(c.id, v, err)
	}
}

func TestEmbeddedConf(t *testing.T) {
	p := t.TempDir() + "/TurboButton.exe"
	os.WriteFile(p, []byte("MZ fake program bytes"), 0755)
	if embeddedConf(p) != "" {
		t.Fatal("plain file shouldn't have settings")
	}
	if err := appendConf(p, "relay=https://r.example\nrelaykey=abc\nstandby=yes\n"); err != nil {
		t.Fatal(err)
	}
	var c relayConf
	parseConf(&c, embeddedConf(p))
	if c.URL != "https://r.example" || c.Key != "abc" || !c.Standby {
		t.Fatalf("got %+v", c)
	}
}
