//go:build unix

package admin

import "os"

func openControllingTerminal() (*os.File, error) {
	// /dev/tty resolves to this process's controlling terminal, regardless of
	// stdout/stderr redirection. It fails for detached/headless processes.
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}
