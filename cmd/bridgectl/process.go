package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func startBridge(meta *metadata) error {
	if _, err := os.Stat(meta.BinaryPath); err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	logFile, err := os.OpenFile(meta.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(meta.BinaryPath, "-c", meta.ConfigPath)
	cmd.Dir = filepath.Dir(meta.ConfigPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	pid := cmd.Process.Pid
	if err = os.WriteFile(meta.PIDPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = logFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}

func stopBridge(meta *metadata) (bool, error) {
	running, pid := processAliveFromPIDFile(meta.PIDPath)
	if !running {
		_ = os.Remove(meta.PIDPath)
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
		if !processAlive(pid) {
			_ = os.Remove(meta.PIDPath)
			return true, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err = proc.Signal(syscall.SIGKILL); err != nil {
		return false, err
	}
	_ = os.Remove(meta.PIDPath)
	return true, nil
}

func processAliveFromPIDFile(path string) (bool, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	return processAlive(pid), pid
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
