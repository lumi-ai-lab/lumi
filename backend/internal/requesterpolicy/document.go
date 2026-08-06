// Package requesterpolicy loads and caches multi-user WeCom requester policies.
package requesterpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pengmide/lumi/internal/requestercontext"
)

const PolicyVersion = 2

// Document is the multi-user permission list file schema (Policy v2).
type Document struct {
	Version int    `json:"version"`
	BotID   string `json:"botId,omitempty"`
	Users   []User `json:"users"`
}

// User is one entry in the permission list.
type User struct {
	UserID        string                              `json:"userId"`
	DisplayName   string                              `json:"displayName"`
	Enabled       bool                                `json:"enabled"`
	Authorization requestercontext.Authorization      `json:"authorization"`
}

type userAuthorizationWire struct {
	Capabilities []string                 `json:"capabilities"`
	Claims       requestercontext.Claims  `json:"claims"`
}

// UnmarshalJSON preserves claims as raw JSON objects.
func (u *User) UnmarshalJSON(data []byte) error {
	var raw struct {
		UserID        string                `json:"userId"`
		DisplayName   string                `json:"displayName"`
		Enabled       bool                  `json:"enabled"`
		Authorization userAuthorizationWire `json:"authorization"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF && err != nil {
		return err
	}
	u.UserID = raw.UserID
	u.DisplayName = raw.DisplayName
	u.Enabled = raw.Enabled
	u.Authorization = requestercontext.Authorization{
		Capabilities: raw.Authorization.Capabilities,
		Claims:       raw.Authorization.Claims,
	}
	return nil
}

// DecodeDocument strictly decodes a single JSON policy document from bytes.
func DecodeDocument(data []byte) (Document, error) {
	var document Document
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("decode requester config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Document{}, errors.New("decode requester config: multiple JSON values are not allowed")
		}
		return Document{}, fmt.Errorf("decode requester config: %w", err)
	}
	return document, nil
}

// NormalizeAndValidate validates and normalizes a policy document in place.
func NormalizeAndValidate(document *Document, expectedBotID string) error {
	if document.Version != PolicyVersion {
		return fmt.Errorf("requester config version must be %d", PolicyVersion)
	}
	document.BotID = strings.TrimSpace(document.BotID)
	if expected := strings.TrimSpace(expectedBotID); document.BotID != "" && expected != "" && document.BotID != expected {
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

		authorization, err := requestercontext.NormalizeAuthorization(user.Authorization)
		if err != nil {
			return fmt.Errorf("requester config users[%d].authorization: %w", i, err)
		}
		user.Authorization = authorization

		if user.Enabled && len(user.Authorization.Capabilities) == 0 {
			return fmt.Errorf("requester config enabled user %q must have at least one capability", user.UserID)
		}
	}
	return nil
}

func cloneUser(user User) User {
	user.Authorization = user.Authorization.Clone()
	return user
}
