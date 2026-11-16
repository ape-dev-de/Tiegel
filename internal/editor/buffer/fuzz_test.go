package buffer

import (
	"bytes"
	"testing"
)

// FuzzBuffer interprets the fuzz input as a sequence of edit operations and
// checks the buffer against a naive []byte reference after each step,
// including undo/redo and the immutability of snapshots.
func FuzzBuffer(f *testing.F) {
	f.Add([]byte("hello\nworld"), []byte{0, 3, 2, 'a', '\n', 1, 0, 4})
	f.Add([]byte("a\r\nb"), []byte{0, 1, 1, '\r', 2, 0, 3, 3, 4, 4})
	f.Add([]byte(""), []byte{0, 0, 3, 'x', 'y', '\n', 0, 0, 1, 'z', 5, 3, 3, 4})
	f.Add([]byte("äöü\n😀"), []byte{1, 2, 3, 0, 5, 2, 'q', 'r'})
	f.Fuzz(func(t *testing.T, initial, ops []byte) {
		if len(initial) > 4096 || len(ops) > 2048 {
			return
		}
		b := New(initial)
		ref := bytes.Clone(initial)
		// history of reference states for undo/redo checks
		undo := [][]byte{}
		redo := [][]byte{}
		snap, snapRef := b.Snapshot(), bytes.Clone(ref)
		next := func() int {
			if len(ops) == 0 {
				return 0
			}
			v := int(ops[0])
			ops = ops[1:]
			return v
		}
		for len(ops) > 0 {
			op := next() % 6
			switch op {
			case 0, 1, 2: // insert / delete / replace
				off := 0
				if len(ref) > 0 {
					off = (next()<<4 ^ next()) % (len(ref) + 1)
				}
				n := 0
				if op != 0 && len(ref)-off > 0 {
					n = next() % (len(ref) - off + 1)
				}
				var ins []byte
				if op != 1 {
					k := next() % 8
					for i := 0; i < k && len(ops) > 0; i++ {
						ins = append(ins, byte(next()))
					}
				}
				b.BreakUndo() // one step per op keeps the reference model simple
				if _, err := b.Replace(off, n, ins); err != nil {
					t.Fatalf("Replace(%d,%d): %v", off, n, err)
				}
				if n > 0 || len(ins) > 0 {
					undo = append(undo, bytes.Clone(ref))
					redo = redo[:0]
				}
				ref = append(ref[:off:off], append(ins, ref[off+n:]...)...)
			case 3:
				_, _, ok := b.Undo()
				if ok != (len(undo) > 0) {
					t.Fatalf("Undo ok=%v with %d steps", ok, len(undo))
				}
				if ok {
					redo = append(redo, ref)
					ref = undo[len(undo)-1]
					undo = undo[:len(undo)-1]
				}
			case 4:
				_, _, ok := b.Redo()
				if ok != (len(redo) > 0) {
					t.Fatalf("Redo ok=%v with %d steps", ok, len(redo))
				}
				if ok {
					undo = append(undo, ref)
					ref = redo[len(redo)-1]
					redo = redo[:len(redo)-1]
				}
			case 5:
				snap, snapRef = b.Snapshot(), bytes.Clone(ref)
			}
			if !bytes.Equal(b.Bytes(), ref) {
				t.Fatalf("content mismatch after op %d:\n got %q\nwant %q", op, b.Bytes(), ref)
			}
		}
		checkAgainst(t, b.Snapshot(), ref)
		checkAgainst(t, snap, snapRef)
	})
}
