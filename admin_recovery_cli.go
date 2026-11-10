package main

import (
	"os"
)

func runAdminRecoveryCommand(args []string) (bool, error) {
	return runAdminAuthCommand(args, os.Stdout)
}
