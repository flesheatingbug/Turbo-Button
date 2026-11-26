//go:build linux

package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestProblemCheckLinux(t *testing.T) {
	r, err := actProblemCheck(context.Background(), "bash network", func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	t.Logf("%.600s", b)
	if _, err := actProblemCheck(context.Background(), "a b", func(string) {}); err == nil {
		t.Fatal("expected an error with no usable keywords")
	}
}
