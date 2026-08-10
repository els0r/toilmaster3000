//go:build darwin || dragonfly || freebsd || netbsd || openbsd || solaris || aix

package harness

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureOpenCodeProcess terminates the CLI's process group where Unix
// process groups are available but Linux's /proc descendant walk is not.
func configureOpenCodeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
