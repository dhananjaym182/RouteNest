package zen

import (
	"log"
	"net/http"
	"regexp"
	"strings"

	"routenest/internal/models"
)

// modelGoneRe matches upstream "model does not exist" error bodies (400/404).
var modelGoneRe = regexp.MustCompile(
	`(?i)model[\s_-]*(not[\s_-]*found|does\s+not\s+exist|no\s+such|unknown|invalid)|(invalid|unknown|no\s+such)[\s_-]*model`)

// IsModelGoneError reports whether an upstream response is an explicit "model
// does not exist" error (400 or 404 with a matching body).
func IsModelGoneError(status int, body string) bool {
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return false
	}
	return modelGoneRe.MatchString(body)
}

// MarkModelGone removes a model after upstream reports it gone, but only when
// the model was marked Delisted by sync. A model that is still on the official
// list may fail for a transient reason, so one failed request never deletes it.
// Custom models are always left for the user to remove.
func MarkModelGone(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if models.RemoveDelisted(model) {
		log.Printf("model %q removed: upstream reports it no longer exists (was delisted)", model)
	}
}
