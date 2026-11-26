package terminal

import (
	"os"
	"strconv"
)

// procCwd returns the working directory of process pid ("" if unknown).
func procCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	d, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return d
}
