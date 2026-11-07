//go:build windows

package admin

import (
	"os"

	"golang.org/x/sys/windows"
)

func openControllingTerminal() (*os.File, error) {
	tty, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(tty.Fd()), &mode); err != nil {
		_ = tty.Close()
		return nil, err
	}
	return tty, nil
}
