package managedruntime

import (
	"fmt"
	"net"
	"os/exec"
)

func AllocateLoopbackURL(scheme string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate loopback %s listener: %w", scheme, err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	if !ok || addr == nil || addr.Port == 0 {
		return "", fmt.Errorf("allocate loopback %s listener: missing TCP port", scheme)
	}
	return fmt.Sprintf("%s://127.0.0.1:%d", scheme, addr.Port), nil
}

type Process struct {
	Cmd *exec.Cmd
}
