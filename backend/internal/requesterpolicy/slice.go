package requesterpolicy

import (
	"encoding/json"
	"fmt"

	"github.com/pengmide/lumi/internal/requestercontext"
)

// SliceUserDocument builds a single-user auth document (metric-cli shape).
func SliceUserDocument(user User) ([]byte, error) {
	if user.UserID == "" {
		return nil, fmt.Errorf("userId is required")
	}
	doc := Document{
		Version: PolicyVersion,
		Users:   []User{cloneUser(user)},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode single-user auth document: %w", err)
	}
	return data, nil
}

// EncryptUser builds a HostAuth blob for one enabled user.
func EncryptUser(user User) (requestercontext.HostAuth, error) {
	plain, err := SliceUserDocument(user)
	if err != nil {
		return requestercontext.HostAuth{}, err
	}
	key, err := ResolveKey()
	if err != nil {
		return requestercontext.HostAuth{}, err
	}
	blob, err := Encrypt(plain, key)
	if err != nil {
		return requestercontext.HostAuth{}, err
	}
	return requestercontext.HostAuth{
		Auth:       blob,
		AuthUserID: user.UserID,
	}, nil
}
