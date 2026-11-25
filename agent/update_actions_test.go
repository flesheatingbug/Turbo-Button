package main

import "testing"

func TestNewUpdateActions(t *testing.T) {
	for _, c := range []struct {
		id string
		p  map[string]any
		ok bool
	}{
		{"driver_updates", map[string]any{"mode": "install", "oemtool": true}, true},
		{"feature_update", map[string]any{"mode": "install", "version": "26H2", "assistant": false}, true},
		{"feature_update", map[string]any{"mode": "install", "version": "26H2; rm"}, false},
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
