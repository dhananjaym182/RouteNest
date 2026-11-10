//go:build desktop

package main

import (
	"log"
	"os"

	"github.com/wailsapp/wails/v2/pkg/logger"
)

type desktopWailsLogger struct{}

var _ logger.Logger = desktopWailsLogger{}

func (desktopWailsLogger) Print(string)   {}
func (desktopWailsLogger) Trace(string)   {}
func (desktopWailsLogger) Debug(string)   {}
func (desktopWailsLogger) Info(string)    {}
func (desktopWailsLogger) Warning(string) {}
func (desktopWailsLogger) Error(string) {
	log.Print("Native desktop runtime reported an error; request details were withheld")
}
func (desktopWailsLogger) Fatal(string) {
	log.Print("Native desktop runtime stopped; request details were withheld")
	os.Exit(1)
}
