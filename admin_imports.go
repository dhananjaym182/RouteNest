package main

import (
	"time"

	"cline-go-proxy/internal/accounts"
	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/i18n"
	"cline-go-proxy/internal/platform"
)

var adminImports = &adminapi.Imports{
	Store:       rootAccountStore{},
	Refresh:     validateNewAccountRefreshToken,
	IsDuplicate: accounts.IsDuplicateImportToken,
	Truncate:    platform.Truncate,
	Now:         time.Now,
	Translate:   i18n.TAPI,
}
