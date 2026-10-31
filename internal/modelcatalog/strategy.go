package modelcatalog

// Fetcher strategies describe how each catalogue fetcher type is handled. Every
// type that reads an OpenAI-shaped list runs through the one generic executor
// (Discover). The status records how far the type is verified, so a reader can
// tell which behaviour rests on evidence.

// Status is the verification level of a fetcher type.
type Status string

const (
	// StatusVerified: an offline fixture from a real captured payload covers the
	// shape RouteNest reads.
	StatusVerified Status = "verified"
	// StatusGeneric: the generic OpenAI-list path is used but no captured fixture
	// exists for the type. Behaviour is preserved, not validated.
	StatusGeneric Status = "generic"
	// StatusUnsupported: the reference catalogue format cannot be read by the
	// generic path, and the preset's catalogue URL is ignored. Discovery returns
	// no models unless the primary /models endpoint works.
	StatusUnsupported Status = "unsupported"
	// StatusSigned: the type is executed by a protocol adapter outside this
	// package (Qoder's COSY-signed catalogue), selected by protocol.
	StatusSigned Status = "signed"
)

// Strategy is the executable description of one fetcher type.
type Strategy struct {
	Type   string
	Status Status
	// Note records the evidence or the known gap.
	Note string
}

var strategies = map[string]Strategy{
	"": {Type: "", Status: StatusVerified,
		Note: "OpenAI-compatible list; fixtures cover data and models envelopes"},
	"openai": {Type: "openai", Status: StatusVerified,
		Note: "OpenAI-compatible list; fixtures cover data and models envelopes"},
	"bynara": {Type: "bynara", Status: StatusVerified,
		Note: "captured /v1/models payload parses; context_window and capability flags are not read"},
	"hcnsec": {Type: "hcnsec", Status: StatusGeneric,
		Note: "reference gateway reads context and capability fields; no captured fixture here"},
	"inxora": {Type: "inxora", Status: StatusGeneric,
		Note: "reference filters accessible and chat flags; the generic path lists every id"},
	"agentrouter": {Type: "agentrouter", Status: StatusUnsupported,
		Note: "reference reads /api/pricing model_name; the catalogue URL is ignored (not a /models URL)"},
	"mimo-free": {Type: "mimo-free", Status: StatusUnsupported,
		Note: "reference reads the models.dev catalogue; the catalogue URL is ignored (not a /models URL)"},
	"opencode-free": {Type: "opencode-free", Status: StatusUnsupported,
		Note: "no preset uses it; the OpenCode zen source has a dedicated transport"},
	"qoder": {Type: "qoder", Status: StatusSigned,
		Note: "COSY-signed /algo/api/v2/model/list through the providers Qoder adapter"},
}

// StrategyFor returns the strategy for a catalogue fetcher type. The empty type
// means the generic OpenAI list.
func StrategyFor(fetcherType string) (Strategy, bool) {
	s, ok := strategies[fetcherType]
	return s, ok
}
