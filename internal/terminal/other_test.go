//go:build !unix

package terminal

func processAlive(int) bool { return false }
