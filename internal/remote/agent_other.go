//go:build !windows

package remote

import (
	"net"
	"os"
)

// dialAgent connects to the running SSH agent, if there is one.
func dialAgent() net.Conn {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return c
}
