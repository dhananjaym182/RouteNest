package main

import (
	"context"
	"fmt"

	"routenest/internal/admin"
)

func runAdminRecoveryCommand(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "auth" {
		return false, nil
	}
	if len(args) != 2 || args[1] != "recover" {
		return true, fmt.Errorf("usage: routenest auth recover (stop RouteNest first; run as the data-directory owner in a local terminal)")
	}
	return true, admin.RecoverSecurityOffline(context.Background())
}
