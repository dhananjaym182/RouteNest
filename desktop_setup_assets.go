//go:build desktop

package main

import (
	"embed"
	"io/fs"
)

//go:embed desktop/setup/*
var embeddedDesktopSetup embed.FS

func desktopSetupAssets() fs.FS {
	assets, err := fs.Sub(embeddedDesktopSetup, "desktop/setup")
	if err != nil {
		panic(err)
	}
	return assets
}
