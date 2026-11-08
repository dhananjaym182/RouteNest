package admin

import (
	"net/http"

	"routenest/internal/adminapi"
	"routenest/internal/providers"
)

var adminModelRemoval = &adminapi.ModelRemoval{Remove: providers.RemoveDelistedModel}

func handleDelistedModelRemoval(w http.ResponseWriter, r *http.Request) {
	adminModelRemoval.ServeHTTP(w, r)
}
