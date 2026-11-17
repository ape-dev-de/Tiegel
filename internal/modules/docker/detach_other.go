//go:build !windows

package docker

import "os/exec"

// prepareDetached adjusts a detached command for the platform: nothing
// to do outside Windows.
func prepareDetached(*exec.Cmd, []string) {}
