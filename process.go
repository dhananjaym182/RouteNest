package main

import "os/exec"

var execCommand = exec.Command

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}
