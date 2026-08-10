//go:build !(linux || darwin || dragonfly || freebsd || netbsd || openbsd || solaris || aix)

package harness

import "os/exec"

// configureOpenCodeProcess relies on exec.CommandContext's direct-process
// cancellation where process groups are unavailable.
func configureOpenCodeProcess(_ *exec.Cmd) {}
