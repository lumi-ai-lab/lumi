package requestercontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var authorizationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)

// NormalizeAuthorization validates Lumi's generic authorization syntax and
// returns an independent value. It does not interpret domain-specific names or
// claim fields.
func NormalizeAuthorization(authorization Authorization) (Authorization, error) {
	capabilities := make([]string, len(authorization.Capabilities))
	seen := make(map[string]struct{}, len(authorization.Capabilities))
	for i, capability := range authorization.Capabilities {
		capability = strings.TrimSpace(capability)
		if !authorizationNamePattern.MatchString(capability) {
			return Authorization{}, fmt.Errorf("capabilities[%d] contains invalid namespaced capability %q", i, capability)
		}
		if _, exists := seen[capability]; exists {
			return Authorization{}, fmt.Errorf("duplicate capability %q", capability)
		}
		seen[capability] = struct{}{}
		capabilities[i] = capability
	}

	claims := make(Claims, len(authorization.Claims))
	for namespace, payload := range authorization.Claims {
		if namespace != strings.TrimSpace(namespace) || !authorizationNamePattern.MatchString(namespace) {
			return Authorization{}, fmt.Errorf("claims contains invalid namespace %q", namespace)
		}
		trimmed := bytes.TrimSpace(payload)
		if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return Authorization{}, fmt.Errorf("claims[%q] must be a JSON object", namespace)
		}
		claims[namespace] = append(json.RawMessage(nil), payload...)
	}

	return Authorization{Capabilities: capabilities, Claims: claims}, nil
}
