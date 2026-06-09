package device

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/lumipaths"
)

func DefaultSecretPath() string {
	return lumipaths.Path("device.secret")
}

func EnsureSecret(path string) (string, error) {
	if path == "" {
		path = DefaultSecretPath()
	}
	if data, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(data))
		if secret == "" {
			return "", errors.New("device secret file is empty")
		}
		return secret, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}

func ValidateBearer(header string, secret string) bool {
	if secret == "" {
		return false
	}
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}
