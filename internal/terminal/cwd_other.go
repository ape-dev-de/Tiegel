//go:build !darwin && !linux

package terminal

// procCwd is not available on this platform: the working directory
// comes from OSC 7 or the start directory.
func procCwd(int) string { return "" }
