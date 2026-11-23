package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Console prints a live, customer-friendly activity log in the agent window.
type Console struct {
	mu    sync.Mutex
	color bool
}

// Retro phosphor palette: bright green for activity, dim green for detail, amber/red for attention.
const (
	cReset  = "\x1b[0m\x1b[32m" // back to the base green, not the console default
	cDim    = "\x1b[32m"
	cCyan   = "\x1b[1;92m"
	cGreen  = "\x1b[92m"
	cRed    = "\x1b[1;91m"
	cYellow = "\x1b[93m"
	cPink   = "\x1b[1;93m"
)

func (c *Console) paint(col, s string) string {
	if !c.color {
		return s
	}
	return col + s + cReset
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func paramText(p map[string]any) string {
	if len(p) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		switch v := p[k].(type) {
		case bool:
			yn := "no"
			if v {
				yn = "yes"
			}
			parts = append(parts, k+": "+yn)
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return " · " + strings.Join(parts, ", ")
}

func (c *Console) Handle(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	switch e.Kind {
	case "phone":
		fmt.Println(c.paint(cPink, ts+"  ◆ Technician's phone connected — watch the work happen here."))
	case "start":
		fmt.Println(c.paint(cCyan, ts+"  ▶ "+e.Job.Title+paramText(e.Job.Params)))
		if e.Job.Reason != "" {
			fmt.Println(c.paint(cDim, "            why: "+clip(e.Job.Reason, 100)))
		}
	case "line":
		fmt.Println(c.paint(cDim, "            "+clip(e.Text, 100)))
	case "done":
		fmt.Println(c.paint(cGreen, ts+"  ✔ "+e.Job.Title+summarize(e.Job)))
	case "failed":
		fmt.Println(c.paint(cRed, ts+"  ✖ "+e.Job.Title+" — "+clip(e.Text, 110)))
	case "queued":
		if !e.Job.Auto && e.Job.Action != "scan" {
			fmt.Println(c.paint(cDim, ts+"  + queued: "+e.Job.Title+paramText(e.Job.Params)))
		}
	}
}

func summarize(j *Job) string {
	var r map[string]any
	if err := jsonUnmarshal(j.Result, &r); err != nil || r == nil {
		return ""
	}
	var bits []string
	if f, ok := r["freedBytes"].(float64); ok && f > 0 {
		bits = append(bits, "freed "+humanBytes(f))
	}
	if v, ok := r["verdict"].(string); ok {
		bits = append(bits, v)
	}
	if n, ok := r["installed"].(float64); ok {
		bits = append(bits, fmt.Sprintf("%d installed", int(n)))
	}
	if n, ok := r["found"].(float64); ok && r["mode"] == "check" {
		bits = append(bits, fmt.Sprintf("%d update(s) available", int(n)))
	}
	if b, ok := r["rebootRequired"].(bool); ok && b {
		bits = append(bits, "restart needed")
	}
	if n, ok := r["note"].(string); ok && n != "" {
		bits = append(bits, n)
	}
	if len(bits) == 0 {
		return ""
	}
	return " — " + clip(strings.Join(bits, " · "), 110)
}

func humanBytes(b float64) string {
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1024 && i < len(u)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", b)
	}
	return fmt.Sprintf("%.1f %s", b, u[i])
}
