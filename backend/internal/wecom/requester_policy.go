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

const requesterPolicyVersion = 1

type requesterPolicyDocument struct {
	Version int                   `json:"version"`
	BotID   string                `json:"botId"`
	Users   []requesterPolicyUser `json:"users"`
}

type requesterPolicyUser struct {
	UserID       string               `json:"userId"`
	DisplayName  string               `json:"displayName"`
	Enabled      bool                 `json:"enabled"`
	Capabilities []string             `json:"capabilities"`
	Scope        requesterPolicyScope `json:"scope"`
}

type requesterPolicyScope struct {
	ManageAreaIDs     []string `json:"manageAreaIds"`
	CategoryLevel1IDs []string `json:"categoryLevel1Ids"`
}

// RequesterPolicy is an immutable snapshot of a validated requester policy.
// It only retains enabled users. Returned requester contexts contain cloned
// slices, so callers cannot mutate the snapshot through a resolved context.
type RequesterPolicy struct {
	botID      string
	revision   string
	enabled    map[string]requesterPolicyUser
	enabledLen int
}

// LoadRequesterPolicy loads and validates one strict JSON policy document.
// When expectedBotID is non-empty, the document must target that exact bot ID.
func LoadRequesterPolicy(path, expectedBotID string) (*RequesterPolicy, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("requester config path is required")
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

	if err := normalizeAndValidateRequesterPolicy(&document, expectedBotID); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	policy := &RequesterPolicy{
		botID:    document.BotID,
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

func normalizeAndValidateRequesterPolicy(document *requesterPolicyDocument, expectedBotID string) error {
	if document.Version != requesterPolicyVersion {
		return fmt.Errorf("requester config version must be %d", requesterPolicyVersion)
	}
	document.BotID = strings.TrimSpace(document.BotID)
	if document.BotID == "" {
		return errors.New("requester config botId is required")
	}
	if expected := strings.TrimSpace(expectedBotID); expected != "" && document.BotID != expected {
		return fmt.Errorf("requester config botId %q does not match configured botId %q", document.BotID, expected)
	}

	seenUserIDs := make(map[string]struct{}, len(document.Users))
	for i := range document.Users {
		user := &document.Users[i]
		user.UserID = strings.TrimSpace(user.UserID)
		user.DisplayName = strings.TrimSpace(user.DisplayName)
		if user.UserID == "" {
			return fmt.Errorf("requester config users[%d].userId is required", i)
		}
		if _, exists := seenUserIDs[user.UserID]; exists {
			return fmt.Errorf("requester config contains duplicate userId %q", user.UserID)
		}
		seenUserIDs[user.UserID] = struct{}{}

		capabilities, err := normalizeCapabilities(user.Capabilities, i)
		if err != nil {
			return err
		}
		user.Capabilities = capabilities

		manageAreaIDs, err := normalizeScopeValues(user.Scope.ManageAreaIDs, i, "manageAreaIds")
		if err != nil {
			return err
		}
		user.Scope.ManageAreaIDs = manageAreaIDs
		categoryLevel1IDs, err := normalizeScopeValues(user.Scope.CategoryLevel1IDs, i, "categoryLevel1Ids")
		if err != nil {
			return err
		}
		user.Scope.CategoryLevel1IDs = categoryLevel1IDs

		if user.Enabled {
			if len(user.Capabilities) == 0 {
				return fmt.Errorf("requester config enabled user %q must have at least one capability", user.UserID)
			}
			if len(user.Scope.ManageAreaIDs) == 0 {
				return fmt.Errorf("requester config enabled user %q must have at least one manageAreaId", user.UserID)
			}
			if len(user.Scope.CategoryLevel1IDs) == 0 {
				return fmt.Errorf("requester config enabled user %q must have at least one categoryLevel1Id", user.UserID)
			}
		}
	}
	return nil
}

func normalizeCapabilities(values []string, userIndex int) ([]string, error) {
	allowed := map[string]struct{}{
		requestercontext.CapabilityCASToken:        {},
		requestercontext.CapabilityCMRQuery:        {},
		requestercontext.CapabilityIndicatorsQuery: {},
		requestercontext.CapabilitySQLSelect:       {},
	}
	result := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			return nil, fmt.Errorf("requester config users[%d].capabilities[%d] contains unknown capability %q", userIndex, i, value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("requester config users[%d] contains duplicate capability %q", userIndex, value)
		}
		seen[value] = struct{}{}
		result[i] = value
	}
	return result, nil
}

func normalizeScopeValues(values []string, userIndex int, field string) ([]string, error) {
	result := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("requester config users[%d].scope.%s[%d] must not be empty", userIndex, field, i)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("requester config users[%d].scope.%s contains duplicate value %q", userIndex, field, value)
		}
		seen[value] = struct{}{}
		result[i] = value
	}
	return result, nil
}

func cloneRequesterPolicyUser(user requesterPolicyUser) requesterPolicyUser {
	user.Capabilities = append([]string(nil), user.Capabilities...)
	user.Scope.ManageAreaIDs = append([]string(nil), user.Scope.ManageAreaIDs...)
	user.Scope.CategoryLevel1IDs = append([]string(nil), user.Scope.CategoryLevel1IDs...)
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
		Version:        requestercontext.CurrentVersion,
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
			Capabilities: append([]string(nil), user.Capabilities...),
			Scope: requestercontext.Scope{
				ManageAreaIDs:     append([]string(nil), user.Scope.ManageAreaIDs...),
				CategoryLevel1IDs: append([]string(nil), user.Scope.CategoryLevel1IDs...),
			},
		},
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
