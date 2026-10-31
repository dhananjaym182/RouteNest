package admin

import (
	"cline-go-proxy/internal/routing"
	"cline-go-proxy/internal/zen"
	"net/http"

	"cline-go-proxy/internal/adminapi"
	"cline-go-proxy/internal/i18n"
	"cline-go-proxy/internal/providers"
)

var adminSync = &adminapi.Sync{
	ModelSync: func() (any, string) {
		res := routing.TriggerModelSync()
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

// POST /admin/api/models/sync — 手动触发一次模型同步
func handleAdminModelSync(w http.ResponseWriter, r *http.Request) {
	adminSync.Models(w, r)
}
