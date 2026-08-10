//go:build linux

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// configureOpenCodeProcess puts every OpenCode run in its own process group.
// OpenCode starts Linux shell tools detached; cancellation therefore walks the
// live descendant tree before killing each detached group and the CLI group.
func configureOpenCodeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		killOpenCodeProcessTree(freezeOpenCodeProcessTree(cmd.Process.Pid))
		return nil
	}
}

// freezeOpenCodeProcessTree stops each process before inspecting its children.
// This closes the fork race between discovering a detached tool and killing its
// group: a stopped process cannot start another child after its descendants are
// collected.
func freezeOpenCodeProcessTree(pid int) []int {
	stopOpenCodeProcess(pid)
	pids := []int{pid}
	for _, child := range openCodeChildPIDs(pid) {
		pids = append(pids, freezeOpenCodeProcessTree(child)...)
	}
	return pids
}

func stopOpenCodeProcess(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGSTOP)
	_ = syscall.Kill(pid, syscall.SIGSTOP)
}

func killOpenCodeProcessTree(pids []int) {
	for i := len(pids) - 1; i >= 0; i-- {
		pid := pids[i]
		// A detached child is its own process-group leader. A regular child is not,
		// so kill its PID too after the group attempt.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func openCodeChildPIDs(pid int) []int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children"))
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(data))
	children := make([]int, 0, len(fields))
	for _, field := range fields {
		child, err := strconv.Atoi(field)
		if err == nil {
			children = append(children, child)
		}
	}
	return children
}
