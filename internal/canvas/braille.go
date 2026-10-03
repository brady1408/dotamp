// Package canvas draws into cell grids using Unicode braille, 2×4 dots per cell.
package canvas

type Braille struct {
	W, H  int
	dots  []uint8
	color []int
}

var dotBit = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

func NewBraille(wCells, hCells int) *Braille {
	b := &Braille{W: wCells, H: hCells}
	b.dots = make([]uint8, wCells*hCells)
	b.color = make([]int, wCells*hCells)
	b.Clear()
	return b
}

func (b *Braille) DotW() int { return b.W * 2 }
func (b *Braille) DotH() int { return b.H * 4 }

func (b *Braille) Clear() {
	for i := range b.dots {
		b.dots[i] = 0
		b.color[i] = -1
	}
}

func (b *Braille) Set(x, y, color int) {
	if x < 0 || y < 0 || x >= b.DotW() || y >= b.DotH() {
		return
	}
	i := (y/4)*b.W + x/2
	b.dots[i] |= dotBit[y%4][x%2]
	b.color[i] = color
}

func (b *Braille) Cell(cx, cy int) (rune, int) {
	i := cy*b.W + cx
	return 0x2800 + rune(b.dots[i]), b.color[i]
}
