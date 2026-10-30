package main

import (
	"cline-go-proxy/internal/zen"
	"net/http"

	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/i18n"
	"cline-go-proxy/internal/providers"
)

var adminSync = &adminapi.Sync{
	ModelSync: func() (any, string) {
		res := triggerModelSync()
		return res, res.Error
	},
	ZenModelSync: func() (any, string) {
		res := zen.SyncModels()
		zen.SetLastModelSync(res)
		return res, res.Error
	},
	RecheckAll: func() any { return providers.RunAllRechecks() },
	RecheckOne: func(id string) (any, error) {
		return providers.RecheckOne(id)
	},
	SyncStatus: providers.SyncStatusSnapshot,
	Translate:  func(r *http.Request, key string) string { return i18n.TAPI(r, key) },
}
