package remote

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialAgent connects to the Windows OpenSSH agent service, if it's running.
func dialAgent() net.Conn {
	timeout := 2 * time.Second
	c, err := winio.DialPipe(`\\.\pipe\openssh-ssh-agent`, &timeout)
	if err != nil {
		return nil
	}
	return c
}
