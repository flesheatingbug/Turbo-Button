package main

import (
	"fmt"
	"os"
	"testing"
)

func writePGM(q *QR, path string) {
	sc, b := 8, 4
	w := (q.Size + 2*b) * sc
	f, _ := os.Create(path)
	fmt.Fprintf(f, "P5 %d %d 255\n", w, w)
	row := make([]byte, w)
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			if q.Get(x/sc-b, y/sc-b) {
				row[x] = 0
			} else {
				row[x] = 255
			}
		}
		f.Write(row)
	}
	f.Close()
}

func TestAll(t *testing.T) {
	os.MkdirAll("/tmp/qr2", 0755)
	for n := 1; n <= 200; n += 3 {
		txt := ""
		for i := 0; len(txt) < n; i++ {
			txt += fmt.Sprintf("%x", i*7919)
		}
		txt = txt[:n]
		q, err := EncodeQR(txt)
		if err != nil {
			break
		}
		writePGM(q, fmt.Sprintf("/tmp/qr2/%03d.pgm", n))
		os.WriteFile(fmt.Sprintf("/tmp/qr2/%03d.txt", n), []byte(txt), 0644)
	}
}
