// Package requestercontext defines the requester identity and authorization
// context propagated from an IM channel to an ACP agent.
package requestercontext

const (
	// CurrentVersion is the current requester context and envelope schema version.
	CurrentVersion = 1

	// EnvRequesterContextDir points agents and hooks at the directory containing
	// session-scoped requester context files.
	EnvRequesterContextDir = "LUMI_REQUESTER_CONTEXT_DIR"

	CapabilityCASToken        = "qdm.cas.token"
	CapabilityCMRQuery        = "qdm.cmr.query"
	CapabilityIndicatorsQuery = "qdm.indicators.query"
	CapabilityMetricQuery     = "qdm.metric.query"
	CapabilitySQLSelect       = "qdm.sql.select"
)

// Context describes the authenticated requester and the authorization snapshot
// that applies to one inbound request.
type Context struct {
	Version        int           `json:"version"`
	RequestID      string        `json:"requestId"`
	PolicyRevision string        `json:"policyRevision"`
	Principal      Principal     `json:"principal"`
	Audience       Audience      `json:"audience"`
	Authorization  Authorization `json:"authorization"`
}

// Principal identifies the requester and the IM bot that authenticated it.
type Principal struct {
	Channel         string `json:"channel"`
	BotID           string `json:"botId"`
	CanonicalUserID string `json:"canonicalUserId"`
	DisplayName     string `json:"displayName"`
}

// Audience identifies the conversation in which the request was made.
type Audience struct {
	ChatID   string `json:"chatId"`
	ChatType string `json:"chatType"`
}

// Authorization is the immutable authorization snapshot for a request.
type Authorization struct {
	Capabilities []string `json:"capabilities"`
	Scope        Scope    `json:"scope"`
}

// Scope contains the business-data dimensions allowed for the requester.
type Scope struct {
	ManageAreaIDs     []string `json:"manageAreaIds"`
	DCManageAreaIDs   []string `json:"dcManageAreaIds"`
	CategoryLevel1IDs []string `json:"categoryLevel1Ids"`
}

// PromptMeta builds the ACP prompt _meta value for a requester context.
// Callers should assign the returned map directly to the prompt's _meta field.
func PromptMeta(requester Context) map[string]any {
	return map[string]any{
		"lumi": map[string]any{
			"requesterContext": requester,
		},
	}
}
