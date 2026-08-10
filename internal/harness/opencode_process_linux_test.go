//go:build linux

package harness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigureOpenCodeProcessCancelsDetachedChild(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is required to exercise detached-child cancellation")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.CommandContext(ctx, "sh", "-c", "setsid sh -c 'echo $$ > "+pidPath+"; sleep 30' & wait")
	configureOpenCodeProcess(cmd)
	require.NoError(t, cmd.Start())

	pid := waitForPID(t, pidPath)
	cancel()
	require.Error(t, cmd.Wait())
	require.Eventually(t, func() bool {
		return processTerminated(pid)
	}, 2*time.Second, 10*time.Millisecond, "detached child must not outlive canceled OpenCode")
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 0
	}, 2*time.Second, 10*time.Millisecond)
	return pid
}

func processTerminated(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	// A killed orphan can remain visible until PID 1 reaps it. It is terminated
	// when /proc reports the Z zombie state even if kill(pid, 0) still succeeds.
	end := strings.LastIndex(string(data), ")")
	return end >= 0 && strings.HasPrefix(string(data[end+1:]), " Z ")
}
