//go:build cgo

package syntax

/*
#include <stddef.h>
void ts_set_allocator(void *(*)(size_t), void *(*)(size_t, size_t), void *(*)(void *, size_t), void (*)(void *));
*/
import "C"

// go-tree-sitter routes every allocation of the Tree-sitter core through
// exported Go functions (go_malloc, go_free, ...) that merely call the C
// allocator again. Each of them is a cgo callback (C → Go → C), which
// dominated parsing time (about half of the CPU time when indexing a
// project). Resetting the allocator to the C defaults keeps the same
// malloc/free pair, so memory allocated before the switch is still freed
// correctly. This runs after go-tree-sitter's own init, before any parser
// exists.
func init() {
	C.ts_set_allocator(nil, nil, nil, nil)
}
