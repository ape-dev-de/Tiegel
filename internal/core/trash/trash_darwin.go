//go:build darwin && cgo

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// trashItem moves path to the trash; on failure it returns 0 and a malloc'ed
// error message in *msg.
static int trashItem(const char *path, char **msg) {
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
		NSError *err = nil;
		if ([[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:nil error:&err]) {
			return 1;
		}
		const char *s = err ? [[err localizedDescription] UTF8String] : "unknown error";
		*msg = strdup(s ? s : "unknown error");
		return 0;
	}
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

func moveToTrash(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	var msg *C.char
	if C.trashItem(cs, &msg) == 0 {
		defer C.free(unsafe.Pointer(msg))
		return fmt.Errorf("trash %s: %s", path, C.GoString(msg))
	}
	return nil
}
