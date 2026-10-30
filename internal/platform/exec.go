package platform

import "os/exec"

// RunCommand starts a command without waiting for it. Used by browser-open
// helpers in packages that cannot depend on package main.
func RunCommand(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}
