package requesterpolicy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// BlobPrefix marks AES-GCM encrypted auth document blobs (metric-cli compatible).
	BlobPrefix = "qdm1enc."

	// EnvAuthBlobKey is base64-encoded 32-byte AES key; overrides the embedded default.
	EnvAuthBlobKey = "QDM_METRIC_AUTH_BLOB_KEY"

	blobAAD       = "qdm-metric-cli/auth-blob/v1"
	blobNonceSize = 12
	blobKeySize   = 32
)

// embeddedDefaultKey matches qdm-metric-cli for early testing / closed binaries.
// Production should set QDM_METRIC_AUTH_BLOB_KEY.
var embeddedDefaultKey = []byte{
	0x71, 0x64, 0x6d, 0x2d, 0x6d, 0x65, 0x74, 0x72,
	0x69, 0x63, 0x2d, 0x61, 0x75, 0x74, 0x68, 0x2d,
	0x62, 0x6c, 0x6f, 0x62, 0x2d, 0x6b, 0x65, 0x79,
	0x21, 0x76, 0x31, 0x2e, 0x30, 0x2d, 0x33, 0x32,
}

// ResolveKey returns the AES-256 key: env base64 key if set, otherwise embedded default.
func ResolveKey() ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv(EnvAuthBlobKey)); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			key, err = base64.RawURLEncoding.DecodeString(raw)
			if err != nil {
				return nil, fmt.Errorf("%s must be base64-encoded 32-byte key: %w", EnvAuthBlobKey, err)
			}
		}
		if len(key) != blobKeySize {
			return nil, fmt.Errorf("%s must decode to %d bytes, got %d", EnvAuthBlobKey, blobKeySize, len(key))
		}
		return key, nil
	}
	return append([]byte(nil), embeddedDefaultKey...), nil
}

// Encrypt seals plaintext auth JSON into a qdm1enc. blob using AES-256-GCM.
func Encrypt(plaintext []byte, key []byte) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("auth blob encrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("auth blob encrypt: %w", err)
	}
	nonce := make([]byte, blobNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("auth blob encrypt: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(blobAAD))
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return BlobPrefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Decrypt opens a qdm1enc. blob and returns plaintext auth JSON bytes.
func Decrypt(blob string, key []byte) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	blob = strings.TrimSpace(blob)
	if blob == "" {
		return nil, fmt.Errorf("auth blob is empty")
	}
	if !strings.HasPrefix(blob, BlobPrefix) {
		return nil, fmt.Errorf("auth blob must start with %s", BlobPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(blob, BlobPrefix))
	if err != nil {
		return nil, fmt.Errorf("auth blob encoding is invalid: %w", err)
	}
	if len(raw) < blobNonceSize+1 {
		return nil, fmt.Errorf("auth blob is too short")
	}
	nonce := raw[:blobNonceSize]
	ciphertext := raw[blobNonceSize:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth blob decrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth blob decrypt: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(blobAAD))
	if err != nil {
		return nil, fmt.Errorf("auth blob decryption failed (wrong key or tampered blob)")
	}
	return plain, nil
}

func validateKey(key []byte) error {
	if len(key) != blobKeySize {
		return fmt.Errorf("auth blob key must be %d bytes", blobKeySize)
	}
	return nil
}
