//go:build desktop

package main

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDesktopLoggerNeverPrintsNativePayloads(t *testing.T) {
	previous := log.Writer()
	defer log.SetOutput(previous)
	var output bytes.Buffer
	log.SetOutput(&output)
	logger := desktopWailsLogger{}
	payload := `process message error: {"password":"private-password","recoveryCode":"private-code","body":"private-session"}`
	for _, call := range []func(string){logger.Print, logger.Trace, logger.Debug, logger.Info, logger.Warning, logger.Error} {
		call(payload)
	}
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), payload) {
		t.Fatal("native logger disclosed a request payload")
	}
	if !strings.Contains(output.String(), "request details were withheld") {
		t.Fatal("native logger did not report a redacted runtime error")
	}
}

func TestDesktopLoggerFatalSuppressesPayloadAndExits(t *testing.T) {
	if os.Getenv("ROUTENEST_TEST_NATIVE_LOGGER_FATAL") == "1" {
		desktopWailsLogger{}.Fatal("private-password private-session private-device-secret")
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDesktopLoggerFatalSuppressesPayloadAndExits$")
	command.Env = append(os.Environ(), "ROUTENEST_TEST_NATIVE_LOGGER_FATAL=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("native fatal logger did not exit")
	}
	if strings.Contains(string(output), "private-") || !strings.Contains(string(output), "request details were withheld") {
		t.Fatal("native fatal logger did not redact the payload")
	}
}
