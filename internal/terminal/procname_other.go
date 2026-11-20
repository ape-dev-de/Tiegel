//go:build unix && !darwin

package terminal

import (
	"os"
	"strconv"
	"strings"
)

// procName returns the command name of process pid ("" if unknown).
func procName(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
