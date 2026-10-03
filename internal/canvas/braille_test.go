package canvas

import "testing"

func TestEmptyCellIsBlankBraille(t *testing.T) {
	b := NewBraille(2, 1)
	r, c := b.Cell(0, 0)
	if r != 0x2800 || c != -1 {
		t.Fatalf("got %U %d", r, c)
	}
}

func TestDotBits(t *testing.T) {
	// Braille dot layout per cell (col,row) -> bit:
	// (0,0)=1 (1,0)=8 / (0,1)=2 (1,1)=16 / (0,2)=4 (1,2)=32 / (0,3)=64 (1,3)=128
	cases := []struct {
		x, y int
		want rune
	}{
		{0, 0, 0x2801}, {1, 0, 0x2808}, {0, 1, 0x2802}, {1, 1, 0x2810},
		{0, 2, 0x2804}, {1, 2, 0x2820}, {0, 3, 0x2840}, {1, 3, 0x2880},
	}
	for _, c := range cases {
		b := NewBraille(1, 1)
		b.Set(c.x, c.y, 2)
		r, col := b.Cell(0, 0)
		if r != c.want || col != 2 {
			t.Errorf("Set(%d,%d) -> %U color %d, want %U", c.x, c.y, r, col, c.want)
		}
	}
}

func TestSecondCellAndOutOfRange(t *testing.T) {
	b := NewBraille(2, 2)
	b.Set(3, 7, 1) // bottom-right dot of cell (1,1)
	if r, _ := b.Cell(1, 1); r != 0x2880 {
		t.Fatalf("got %U", r)
	}
	b.Set(-1, 0, 1)
	b.Set(4, 0, 1)
	b.Set(0, 8, 1)
	if r, _ := b.Cell(0, 0); r != 0x2800 {
		t.Fatal("out of range Set must be ignored")
	}
	b.Clear()
	if r, c := b.Cell(1, 1); r != 0x2800 || c != -1 {
		t.Fatal("Clear must reset dots and colors")
	}
}

func TestFullColumn(t *testing.T) {
	b := NewBraille(1, 1)
	for y := 0; y < 4; y++ {
		b.Set(0, y, 3)
		b.Set(1, y, 3)
	}
	if r, _ := b.Cell(0, 0); r != 0x28FF {
		t.Fatalf("got %U", r)
	}
}
