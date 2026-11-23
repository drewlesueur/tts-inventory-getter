package scrape

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// A shell or driver can outlive its direct child. Signal the entire process
// group on cancellation and bound waits on pipes inherited by descendants.
func manageChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	cmd.WaitDelay = 2 * time.Second
}

func cleanChildProcessGroup(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}
	group := -cmd.Process.Pid
	if err := syscall.Kill(group, syscall.SIGKILL); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(group, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
