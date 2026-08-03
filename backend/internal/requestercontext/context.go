// Package requestercontext defines the requester identity and authorization
// context propagated from an IM channel to an ACP agent.
package requestercontext

import "encoding/json"

const (
	// CurrentContextVersion is the requester context schema version.
	CurrentContextVersion = 2

	// CurrentEnvelopeVersion is the on-disk envelope schema version. The
	// requester context nested inside the envelope is versioned independently.
	CurrentEnvelopeVersion = 1

	// EnvRequesterContextDir points agents and hooks at the directory containing
	// session-scoped requester context files.
	EnvRequesterContextDir = "LUMI_REQUESTER_CONTEXT_DIR"
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
	Claims       Claims   `json:"claims"`
}

// Claims contains opaque, namespace-scoped authorization data. Lumi preserves
// each JSON object without interpreting its domain-specific fields.
type Claims map[string]json.RawMessage

// Clone returns an independent authorization value with non-nil collections.
func (authorization Authorization) Clone() Authorization {
	capabilities := make([]string, len(authorization.Capabilities))
	copy(capabilities, authorization.Capabilities)
	return Authorization{
		Capabilities: capabilities,
		Claims:       authorization.Claims.Clone(),
	}
}

// Clone returns an independent copy of every opaque claim payload.
func (claims Claims) Clone() Claims {
	cloned := make(Claims, len(claims))
	for namespace, payload := range claims {
		cloned[namespace] = append(json.RawMessage(nil), payload...)
	}
	return cloned
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
