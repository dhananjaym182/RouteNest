package admin

import (
	"time"

	"routenest/internal/accounts"
	"routenest/internal/adminapi"
	"routenest/internal/i18n"
	"routenest/internal/platform"
)

var adminImports = &adminapi.Imports{
	Store:       rootAccountStore{},
	Refresh:     validateNewAccountRefreshToken,
	IsDuplicate: accounts.IsDuplicateImportToken,
	Truncate:    platform.Truncate,
	Now:         time.Now,
	Translate:   i18n.TAPI,
}
