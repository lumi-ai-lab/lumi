package wecom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pengmide/lumi/internal/requestercontext"
)

const requesterPolicyVersion = 2

type requesterPolicyDocument struct {
	Version int                   `json:"version"`
	BotID   string                `json:"botId,omitempty"`
	Users   []requesterPolicyUser `json:"users"`
}

type requesterPolicyUser struct {
	UserID        string                       `json:"userId"`
	DisplayName   string                       `json:"displayName"`
	Enabled       bool                         `json:"enabled"`
	Authorization requesterPolicyAuthorization `json:"authorization"`
}

type requesterPolicyAuthorization struct {
	Capabilities []string                `json:"capabilities"`
	Claims       requestercontext.Claims `json:"claims"`
}

// RequesterPolicy is an immutable snapshot of a validated requester policy.
// It only retains enabled users. Returned requester contexts contain cloned
// collections, so callers cannot mutate the snapshot through a resolved context.
type RequesterPolicy struct {
	botID      string
	revision   string
	enabled    map[string]requesterPolicyUser
	enabledLen int
}

// LoadRequesterPolicy loads and validates one strict JSON policy document.
// botId is an optional audience constraint: when both the document and runtime
// BotIDs are non-empty, they must match. The runtime BotID remains the source of
// truth for RequesterContext principals.
func LoadRequesterPolicy(path, runtimeBotID string) (*RequesterPolicy, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("requester config path is required")
	}
	runtimeBotID = strings.TrimSpace(runtimeBotID)
	if runtimeBotID == "" {
		return nil, errors.New("runtime bot id is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load requester config: %w", err)
	}

	var document requesterPolicyDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode requester config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode requester config: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode requester config: %w", err)
	}

	if err := normalizeAndValidateRequesterPolicy(&document, runtimeBotID); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	policy := &RequesterPolicy{
		botID:    runtimeBotID,
		revision: "sha256:" + hex.EncodeToString(hash[:]),
		enabled:  make(map[string]requesterPolicyUser),
	}
	for _, user := range document.Users {
		if !user.Enabled {
			continue
		}
		policy.enabled[user.UserID] = cloneRequesterPolicyUser(user)
	}
	policy.enabledLen = len(policy.enabled)
	return policy, nil
}

func cloneRequesterPolicyUser(user requesterPolicyUser) requesterPolicyUser {
	cloned := requestercontext.Authorization{
		Capabilities: user.Authorization.Capabilities,
		Claims:       user.Authorization.Claims,
	}.Clone()
	user.Authorization.Capabilities = cloned.Capabilities
	user.Authorization.Claims = cloned.Claims
	return user
}

// BuildContext resolves an enabled user after trimming surrounding whitespace.
// User IDs remain case-sensitive. Unknown and disabled users return false.
func (p *RequesterPolicy) BuildContext(userID, requestID, chatID, chatType string) (*requestercontext.Context, bool) {
	if p == nil {
		return nil, false
	}
	user, ok := p.enabled[strings.TrimSpace(userID)]
	if !ok {
		return nil, false
	}
	ctx := &requestercontext.Context{
		Version:        requestercontext.CurrentContextVersion,
		RequestID:      strings.TrimSpace(requestID),
		PolicyRevision: p.revision,
		Principal: requestercontext.Principal{
			Channel:         "wecom",
			BotID:           p.botID,
			CanonicalUserID: user.UserID,
			DisplayName:     user.DisplayName,
		},
		Audience: requestercontext.Audience{
			ChatID:   strings.TrimSpace(chatID),
			ChatType: strings.TrimSpace(chatType),
		},
		Authorization: requestercontext.Authorization{
			Capabilities: user.Authorization.Capabilities,
			Claims:       user.Authorization.Claims,
		}.Clone(),
	}
	return ctx, true
}

func (p *RequesterPolicy) BotID() string {
	if p == nil {
		return ""
	}
	return p.botID
}

func (p *RequesterPolicy) Revision() string {
	if p == nil {
		return ""
	}
	return p.revision
}

func (p *RequesterPolicy) EnabledUserCount() int {
	if p == nil {
		return 0
	}
	return p.enabledLen
}
