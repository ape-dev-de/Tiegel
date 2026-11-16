package buffer

import "testing"

func TestColumnConversions(t *testing.T) {
	line := []byte("aä😀éx") // a, ä(2), 😀(4), e+combining(3), x
	if got := RuneCol(line, 3); got != 2 {
		t.Errorf("RuneCol = %d", got)
	}
	if got := ByteColFromRunes(line, 3); got != 7 {
		t.Errorf("ByteColFromRunes = %d", got)
	}
	if got := ByteColFromRunes(line, 99); got != len(line) {
		t.Errorf("ByteColFromRunes clamp = %d", got)
	}
	if got := UTF16Col(line, 7); got != 4 {
		t.Errorf("UTF16Col = %d", got)
	}
	if got := ByteColFromUTF16(line, 4); got != 7 {
		t.Errorf("ByteColFromUTF16 = %d", got)
	}
	if got := GraphemeCol(line, 10); got != 4 {
		t.Errorf("GraphemeCol = %d", got)
	}
	if got := GraphemeCol(line, 8); got != 4 { // inside e+combining
		t.Errorf("GraphemeCol inside = %d", got)
	}
	if got := ByteColFromGraphemes(line, 4); got != 10 {
		t.Errorf("ByteColFromGraphemes = %d", got)
	}
	if got := RuneCol(line, -4); got != 0 {
		t.Errorf("RuneCol clamp = %d", got)
	}
}

func TestVisualColumns(t *testing.T) {
	line := []byte("\tab日x")
	cases := []struct{ byteCol, vcol int }{{0, 0}, {1, 4}, {2, 5}, {3, 6}, {6, 8}, {7, 9}}
	for _, c := range cases {
		if got := VisualCol(line, c.byteCol, 4); got != c.vcol {
			t.Errorf("VisualCol(%d) = %d want %d", c.byteCol, got, c.vcol)
		}
		if got := ByteColFromVisual(line, c.vcol, 4); got != c.byteCol {
			t.Errorf("ByteColFromVisual(%d) = %d want %d", c.vcol, got, c.byteCol)
		}
	}
	// Inside the tab and inside the wide char round down.
	if got := ByteColFromVisual(line, 2, 4); got != 0 {
		t.Errorf("inside tab = %d", got)
	}
	if got := ByteColFromVisual(line, 7, 4); got != 3 {
		t.Errorf("inside wide = %d", got)
	}
	if got := ByteColFromVisual(line, 99, 0); got != len(line) {
		t.Errorf("beyond = %d", got)
	}
	if got := VisualCol(line, 1, 0); got != 4 {
		t.Errorf("default tab width = %d", got)
	}
}

func TestGraphemeNavigation(t *testing.T) {
	s := NewString("a😀\r\né\nz").Snapshot()
	// offsets: a=0, 😀=1..5, \r=5, \n=6, e=7, combining=8..10, \n=10, z=11
	fwd := []int{0, 1, 5, 7, 10, 11, 12, 12}
	for i := 0; i+1 < len(fwd); i++ {
		if got := s.NextGrapheme(fwd[i]); got != fwd[i+1] {
			t.Errorf("NextGrapheme(%d) = %d want %d", fwd[i], got, fwd[i+1])
		}
	}
	back := []int{12, 11, 10, 7, 5, 1, 0, 0}
	for i := 0; i+1 < len(back); i++ {
		if got := s.PrevGrapheme(back[i]); got != back[i+1] {
			t.Errorf("PrevGrapheme(%d) = %d want %d", back[i], got, back[i+1])
		}
	}
	if got := s.PrevGrapheme(6); got != 5 {
		t.Errorf("PrevGrapheme between CR LF = %d", got)
	}
	if got := s.NextGrapheme(6); got != 7 {
		t.Errorf("NextGrapheme between CR LF = %d", got)
	}
}
