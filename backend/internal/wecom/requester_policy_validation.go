package wecom

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pengmide/lumi/internal/requestercontext"
)

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

		authorization, err := requestercontext.NormalizeAuthorization(requestercontext.Authorization{
			Capabilities: user.Authorization.Capabilities,
			Claims:       user.Authorization.Claims,
		})
		if err != nil {
			return fmt.Errorf("requester config users[%d].authorization: %w", i, err)
		}
		user.Authorization.Capabilities = authorization.Capabilities
		user.Authorization.Claims = authorization.Claims

		if user.Enabled && len(user.Authorization.Capabilities) == 0 {
			return fmt.Errorf("requester config enabled user %q must have at least one capability", user.UserID)
		}
	}
	return nil
}
