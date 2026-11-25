package main

// Minimal QR Code encoder (byte mode, error correction level M, versions 1-10).
// Based on the structure of Project Nayuki's reference implementation (MIT).

import (
	"errors"
	"fmt"
	"strings"
)

var eccPerBlockM = [11]int{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
var numBlocksM = [11]int{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}

type QR struct {
	Size    int
	modules [][]bool
	isFunc  [][]bool
}

func (q *QR) Get(x, y int) bool {
	if x < 0 || y < 0 || x >= q.Size || y >= q.Size {
		return false
	}
	return q.modules[y][x]
}

func numRawDataModules(ver int) int {
	r := (16*ver+128)*ver + 64
	if ver >= 2 {
		na := ver/7 + 2
		r -= (25*na-10)*na - 55
		if ver >= 7 {
			r -= 36
		}
	}
	return r
}

func numDataCodewords(ver int) int {
	return numRawDataModules(ver)/8 - eccPerBlockM[ver]*numBlocksM[ver]
}

func gfMul(x, y int) int {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= ((y >> i) & 1) * x
	}
	return z
}

func rsDivisor(degree int) []int {
	res := make([]int, degree)
	res[degree-1] = 1
	root := 1
	for i := 0; i < degree; i++ {
		for j := 0; j < degree; j++ {
			res[j] = gfMul(res[j], root)
			if j+1 < degree {
				res[j] ^= res[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return res
}

func rsRemainder(data, div []int) []int {
	res := make([]int, len(div))
	for _, b := range data {
		f := b ^ res[0]
		copy(res, res[1:])
		res[len(res)-1] = 0
		for i, c := range div {
			res[i] ^= gfMul(c, f)
		}
	}
	return res
}

// EncodeQR encodes text into a QR code.
func EncodeQR(text string) (*QR, error) {
	data := []byte(text)
	ver := 0
	for v := 1; v <= 10; v++ {
		cc := 8
		if v >= 10 {
			cc = 16
		}
		if 4+cc+8*len(data) <= numDataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, errors.New("text too long for QR")
	}
	cc := 8
	if ver >= 10 {
		cc = 16
	}
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1 == 1)
		}
	}
	put(0x4, 4)
	put(len(data), cc)
	for _, b := range data {
		put(int(b), 8)
	}
	capBits := numDataCodewords(ver) * 8
	t := capBits - len(bits)
	if t > 4 {
		t = 4
	}
	put(0, t)
	put(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capBits; pad ^= 0xEC ^ 0x11 {
		put(pad, 8)
	}
	cw := make([]int, len(bits)/8)
	for i, b := range bits {
		if b {
			cw[i>>3] |= 1 << (7 - uint(i&7))
		}
	}

	// Split into blocks, add ECC, interleave.
	nb := numBlocksM[ver]
	ecl := eccPerBlockM[ver]
	raw := numRawDataModules(ver) / 8
	nShort := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(ecl)
	var blocks [][]int
	k := 0
	for i := 0; i < nb; i++ {
		dl := shortLen - ecl
		if i >= nShort {
			dl++
		}
		dat := append([]int{}, cw[k:k+dl]...)
		k += dl
		ecc := rsRemainder(dat, div)
		if i < nShort {
			dat = append(dat, 0)
		}
		blocks = append(blocks, append(dat, ecc...))
	}
	var final []int
	for i := 0; i < len(blocks[0]); i++ {
		for j, blk := range blocks {
			if i != shortLen-ecl || j >= nShort {
				final = append(final, blk[i])
			}
		}
	}

	size := ver*4 + 17
	best := (*QR)(nil)
	bestPen := -1
	for mask := 0; mask < 8; mask++ {
		q := newQR(size)
		q.drawFunction(ver)
		q.drawCodewords(final)
		q.applyMask(mask)
		q.drawFormat(mask)
		p := q.penalty()
		if bestPen < 0 || p < bestPen {
			best, bestPen = q, p
		}
	}
	return best, nil
}

func newQR(size int) *QR {
	q := &QR{Size: size}
	q.modules = make([][]bool, size)
	q.isFunc = make([][]bool, size)
	for i := range q.modules {
		q.modules[i] = make([]bool, size)
		q.isFunc[i] = make([]bool, size)
	}
	return q
}

func (q *QR) setF(x, y int, dark bool) {
	q.modules[y][x] = dark
	q.isFunc[y][x] = true
}

func (q *QR) drawFunction(ver int) {
	s := q.Size
	for i := 0; i < s; i++ {
		q.setF(6, i, i%2 == 0)
		q.setF(i, 6, i%2 == 0)
	}
	finder := func(cx, cy int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := cx+dx, cy+dy
				if x < 0 || y < 0 || x >= s || y >= s {
					continue
				}
				d := max(abs(dx), abs(dy))
				q.setF(x, y, d != 2 && d != 4)
			}
		}
	}
	finder(3, 3)
	finder(s-4, 3)
	finder(3, s-4)
	pos := alignPositions(ver, s)
	n := len(pos)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if (i == 0 && j == 0) || (i == 0 && j == n-1) || (i == n-1 && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					q.setF(pos[i]+dx, pos[j]+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	q.drawFormat(0) // reserve
	if ver >= 7 {
		rem := ver
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		b := ver<<12 | rem
		for i := 0; i < 18; i++ {
			bit := (b>>i)&1 == 1
			a := s - 11 + i%3
			c := i / 3
			q.setF(a, c, bit)
			q.setF(c, a, bit)
		}
	}
}

func alignPositions(ver, size int) []int {
	if ver == 1 {
		return nil
	}
	na := ver/7 + 2
	step := (ver*8 + na*3 + 5) / (na*4 - 4) * 2
	res := []int{6}
	var rest []int
	for p := size - 7; len(rest)+1 < na; p -= step {
		rest = append([]int{p}, rest...)
	}
	return append(res, rest...)
}

func (q *QR) drawFormat(mask int) {
	data := 0<<3 | mask // ECC M = 0b00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	b := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (b>>i)&1 == 1 }
	s := q.Size
	for i := 0; i <= 5; i++ {
		q.setF(8, i, bit(i))
	}
	q.setF(8, 7, bit(6))
	q.setF(8, 8, bit(7))
	q.setF(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		q.setF(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		q.setF(s-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		q.setF(8, s-15+i, bit(i))
	}
	q.setF(8, s-8, true)
}

func (q *QR) drawCodewords(data []int) {
	s := q.Size
	i := 0
	for right := s - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < s; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				up := (right+1)&2 == 0
				y := vert
				if up {
					y = s - 1 - vert
				}
				if !q.isFunc[y][x] && i < len(data)*8 {
					q.modules[y][x] = (data[i>>3]>>(7-uint(i&7)))&1 == 1
					i++
				}
			}
		}
	}
}

func (q *QR) applyMask(m int) {
	for y := 0; y < q.Size; y++ {
		for x := 0; x < q.Size; x++ {
			var inv bool
			switch m {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if !q.isFunc[y][x] && inv {
				q.modules[y][x] = !q.modules[y][x]
			}
		}
	}
}

// Simplified penalty (runs, 2x2 blocks, balance) — any mask decodes; this just avoids ugly ones.
func (q *QR) penalty() int {
	s, p := q.Size, 0
	for y := 0; y < s; y++ {
		for _, horiz := range []bool{true, false} {
			run, last := 0, false
			for x := 0; x < s; x++ {
				v := q.modules[y][x]
				if !horiz {
					v = q.modules[x][y]
				}
				if x > 0 && v == last {
					run++
					if run == 5 {
						p += 3
					} else if run > 5 {
						p++
					}
				} else {
					run, last = 1, v
				}
			}
		}
	}
	dark := 0
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			if q.modules[y][x] {
				dark++
			}
			if x < s-1 && y < s-1 {
				c := q.modules[y][x]
				if c == q.modules[y][x+1] && c == q.modules[y+1][x] && c == q.modules[y+1][x+1] {
					p += 3
				}
			}
		}
	}
	total := s * s
	k := (abs(dark*20-total*10) + total - 1) / total
	return p + (k-1)*10
}

// Terminal renders the code with half-block characters. Light modules are drawn
// as full blocks so a console with a dark background shows a normal dark-on-light code.
func (q *QR) Terminal() string {
	var sb strings.Builder
	border := 2
	light := func(x, y int) bool { return !q.Get(x, y) }
	for y := -border; y < q.Size+border; y += 2 {
		for x := -border; x < q.Size+border; x++ {
			t, b := light(x, y), light(x, y+1)
			if y+1 >= q.Size+border {
				b = false
			}
			switch {
			case t && b:
				sb.WriteString("█")
			case t:
				sb.WriteString("▀")
			case b:
				sb.WriteString("▄")
			default:
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// SVG renders the code as a crisp, scalable image (black on white, 4-module quiet zone).
func (q *QR) SVG() string {
	var sb strings.Builder
	n := q.Size + 8
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := 0; y < q.Size; y++ {
		for x := 0; x < q.Size; x++ {
			if q.Get(x, y) {
				fmt.Fprintf(&sb, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	sb.WriteString(`"/></svg>`)
	return sb.String()
}
