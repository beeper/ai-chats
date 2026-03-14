package bridgeutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartBridge launches a bridge process in the background, redirecting its
// stdout and stderr to logPath. The process PID is written to pidPath.
// The command is specified as the executable path plus any additional
// arguments (e.g., "-c", configPath).
func StartBridge(exe string, args []string, workDir, logPath, pidPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	pid := cmd.Process.Pid
	if err = os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = logFile.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}

// StartBridgeFromConfig is a convenience wrapper around StartBridge that
// derives the working directory from the config path.
func StartBridgeFromConfig(exe string, args []string, configPath, logPath, pidPath string) error {
	return StartBridge(exe, args, filepath.Dir(configPath), logPath, pidPath)
}

// StopByPIDFile reads a PID from pidPath, sends SIGTERM to the process,
// waits up to 5 seconds for it to exit, then sends SIGKILL if needed.
// Returns true if the process was running and was stopped.
func StopByPIDFile(pidPath string) (bool, error) {
	running, pid := ProcessAliveFromPIDFile(pidPath)
	if !running {
		_ = os.Remove(pidPath)
		return false, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	if err = proc.Signal(syscall.SIGTERM); err != nil {
		return false, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			_ = os.Remove(pidPath)
			return true, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err = proc.Signal(syscall.SIGKILL); err != nil {
		return false, err
	}
	_ = os.Remove(pidPath)
	return true, nil
}

// ProcessAliveFromPIDFile reads a PID from the given file and checks whether
// the corresponding process is running.
func ProcessAliveFromPIDFile(path string) (bool, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	return ProcessAlive(pid), pid
}

// ProcessAlive checks whether a process with the given PID is running by
// sending signal 0.
func ProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
