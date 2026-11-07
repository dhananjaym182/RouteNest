package main

import "testing"

func TestRecoveryCommandRejectsAmbiguousArgumentsBeforeStartup(t *testing.T) {
	for _, args := range [][]string{{"auth"}, {"auth", "reset"}, {"auth", "recover", "password"}, {"auth", "recover", "--host", "0.0.0.0"}} {
		if handled, err := runAdminRecoveryCommand(args); !handled || err == nil {
			t.Fatalf("recovery command %v = handled %v, err %v", args, handled, err)
		}
	}
	for _, args := range [][]string{nil, {"--port", "3457"}} {
		if handled, err := runAdminRecoveryCommand(args); handled || err != nil {
			t.Fatalf("ordinary command %v = handled %v, err %v", args, handled, err)
		}
	}
}
