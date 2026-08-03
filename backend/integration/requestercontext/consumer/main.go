package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	requesterctx "github.com/pengmide/lumi/internal/requestercontext"
)

const (
	defaultCapability     = "qdm.cmr.query"
	defaultClaimNamespace = "qdm.scope"
)

type consumerConfig struct {
	ContextDir       string
	SessionID        string
	WorkspaceID      string
	AgentID          string
	Capability       string
	ClaimNamespace   string
	ManageAreaID     string
	DCManageAreaID   string
	CategoryLevel1ID string
}

type qdmScopeClaim struct {
	SchemaVersion     int      `json:"schemaVersion"`
	ManageAreaIDs     []string `json:"manageAreaIds"`
	DCManageAreaIDs   []string `json:"dcManageAreaIds"`
	CategoryLevel1IDs []string `json:"categoryLevel1Ids"`
}

type decision struct {
	Allowed              bool   `json:"allowed"`
	ContextVersion       int    `json:"contextVersion"`
	Capability           string `json:"capability"`
	ClaimNamespace       string `json:"claimNamespace"`
	PrincipalFingerprint string `json:"principalFingerprint"`
	ManageAreaID         string `json:"manageAreaId,omitempty"`
	DCManageAreaID       string `json:"dcManageAreaId,omitempty"`
	CategoryLevel1ID     string `json:"categoryLevel1Id"`
}

type deniedDecision struct {
	Allowed bool   `json:"allowed"`
	Error   string `json:"error"`
}

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		writeDenied(err)
		os.Exit(2)
	}
	result, err := consume(cfg, time.Now().UTC())
	if err != nil {
		writeDenied(err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, `{"allowed":false,"error":"encode decision"}`)
		os.Exit(1)
	}
}

func writeDenied(err error) {
	_ = json.NewEncoder(os.Stderr).Encode(deniedDecision{Allowed: false, Error: err.Error()})
}

func parseConfig(args []string, getenv func(string) string) (consumerConfig, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := consumerConfig{}
	flags := flag.NewFlagSet("requester-context-consumer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.ContextDir, "context-dir", strings.TrimSpace(getenv(requesterctx.EnvRequesterContextDir)), "requester context directory")
	flags.StringVar(&cfg.SessionID, "session-id", "", "raw ACP session ID")
	flags.StringVar(&cfg.WorkspaceID, "workspace-id", strings.TrimSpace(getenv("LUMI_WORKSPACE_ID")), "expected workspace ID")
	flags.StringVar(&cfg.AgentID, "agent-id", "pi", "expected agent ID")
	flags.StringVar(&cfg.Capability, "capability", defaultCapability, "required capability")
	flags.StringVar(&cfg.ClaimNamespace, "claim-namespace", defaultClaimNamespace, "required claim namespace")
	flags.StringVar(&cfg.ManageAreaID, "manage-area-id", "", "requested manage area ID")
	flags.StringVar(&cfg.DCManageAreaID, "dc-manage-area-id", "", "requested DC manage area ID")
	flags.StringVar(&cfg.CategoryLevel1ID, "category-level1-id", "", "requested category level 1 ID")
	if err := flags.Parse(args); err != nil {
		return consumerConfig{}, fmt.Errorf("parse arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return consumerConfig{}, errors.New("positional arguments are not allowed")
	}

	cfg.ContextDir = strings.TrimSpace(cfg.ContextDir)
	// SessionID is an opaque protocol identity. Do not trim or normalize it.
	cfg.WorkspaceID = strings.TrimSpace(cfg.WorkspaceID)
	cfg.AgentID = strings.TrimSpace(cfg.AgentID)
	cfg.Capability = strings.TrimSpace(cfg.Capability)
	cfg.ClaimNamespace = strings.TrimSpace(cfg.ClaimNamespace)
	cfg.ManageAreaID = strings.TrimSpace(cfg.ManageAreaID)
	cfg.DCManageAreaID = strings.TrimSpace(cfg.DCManageAreaID)
	cfg.CategoryLevel1ID = strings.TrimSpace(cfg.CategoryLevel1ID)

	switch {
	case cfg.ContextDir == "":
		return consumerConfig{}, errors.New("requester context directory is required")
	case !filepath.IsAbs(cfg.ContextDir):
		return consumerConfig{}, errors.New("requester context directory must be absolute")
	case cfg.SessionID == "":
		return consumerConfig{}, errors.New("raw ACP session ID is required")
	case cfg.WorkspaceID == "":
		return consumerConfig{}, errors.New("expected workspace ID is required")
	case cfg.AgentID == "":
		return consumerConfig{}, errors.New("expected agent ID is required")
	case cfg.Capability == "":
		return consumerConfig{}, errors.New("required capability is empty")
	case cfg.ClaimNamespace == "":
		return consumerConfig{}, errors.New("claim namespace is empty")
	case cfg.ManageAreaID == "" && cfg.DCManageAreaID == "":
		return consumerConfig{}, errors.New("manage-area-id or dc-manage-area-id is required")
	case cfg.CategoryLevel1ID == "":
		return consumerConfig{}, errors.New("category-level1-id is required")
	}
	return cfg, nil
}

func consume(cfg consumerConfig, now time.Time) (decision, error) {
	envelope, err := loadEnvelope(cfg.ContextDir, cfg.SessionID)
	if err != nil {
		return decision{}, err
	}
	if err := validateEnvelope(envelope, cfg, now); err != nil {
		return decision{}, err
	}

	claimPayload, ok := envelope.RequesterContext.Authorization.Claims[cfg.ClaimNamespace]
	if !ok {
		return decision{}, fmt.Errorf("required claim namespace %q is missing", cfg.ClaimNamespace)
	}
	claim, err := decodeQDMClaim(claimPayload)
	if err != nil {
		return decision{}, err
	}
	if cfg.ManageAreaID != "" && !contains(claim.ManageAreaIDs, cfg.ManageAreaID) {
		return decision{}, errors.New("requested manage area is outside the authorized scope")
	}
	if cfg.DCManageAreaID != "" && !contains(claim.DCManageAreaIDs, cfg.DCManageAreaID) {
		return decision{}, errors.New("requested DC manage area is outside the authorized scope")
	}
	if !contains(claim.CategoryLevel1IDs, cfg.CategoryLevel1ID) {
		return decision{}, errors.New("requested category is outside the authorized scope")
	}

	principalHash := sha256.Sum256([]byte(envelope.RequesterContext.Principal.CanonicalUserID))
	return decision{
		Allowed:              true,
		ContextVersion:       envelope.RequesterContext.Version,
		Capability:           cfg.Capability,
		ClaimNamespace:       cfg.ClaimNamespace,
		PrincipalFingerprint: "sha256:" + hex.EncodeToString(principalHash[:])[:16],
		ManageAreaID:         cfg.ManageAreaID,
		DCManageAreaID:       cfg.DCManageAreaID,
		CategoryLevel1ID:     cfg.CategoryLevel1ID,
	}, nil
}

func loadEnvelope(dir, sessionID string) (requesterctx.Envelope, error) {
	name, err := requesterctx.SessionFileName(sessionID)
	if err != nil {
		return requesterctx.Envelope{}, err
	}
	payload, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return requesterctx.Envelope{}, fmt.Errorf("read requester context envelope for exact session: %w", err)
	}
	var envelope requesterctx.Envelope
	if err := decodeStrictJSON(payload, &envelope); err != nil {
		return requesterctx.Envelope{}, fmt.Errorf("decode requester context envelope: %w", err)
	}
	return envelope, nil
}

func validateEnvelope(envelope requesterctx.Envelope, cfg consumerConfig, now time.Time) error {
	ctx := envelope.RequesterContext
	switch {
	case envelope.Version != requesterctx.CurrentEnvelopeVersion:
		return fmt.Errorf("unsupported envelope version %d", envelope.Version)
	case ctx.Version != requesterctx.CurrentContextVersion:
		return fmt.Errorf("unsupported requester context version %d", ctx.Version)
	case envelope.WorkspaceID != cfg.WorkspaceID:
		return errors.New("requester context workspace binding mismatch")
	case envelope.AgentID != cfg.AgentID:
		return errors.New("requester context agent binding mismatch")
	case envelope.SessionID != cfg.SessionID:
		return errors.New("requester context session binding mismatch")
	case envelope.IssuedAt.IsZero() || envelope.IssuedAt.After(now.Add(time.Minute)):
		return errors.New("requester context issue time is invalid")
	case envelope.ExpiresAt.IsZero() || !envelope.ExpiresAt.After(now):
		return errors.New("requester context envelope is expired")
	case !envelope.ExpiresAt.After(envelope.IssuedAt) || envelope.ExpiresAt.Sub(envelope.IssuedAt) > requesterctx.DefaultTTL:
		return errors.New("requester context TTL is invalid")
	case ctx.RequestID == "":
		return errors.New("requester context request ID is empty")
	case !validPolicyRevision(ctx.PolicyRevision):
		return errors.New("requester context policy revision is invalid")
	case ctx.Principal.Channel != "wecom":
		return errors.New("requester context channel is not wecom")
	case ctx.Principal.BotID == "" || ctx.Principal.CanonicalUserID == "":
		return errors.New("requester context principal is incomplete")
	case ctx.Audience.ChatID == "" || ctx.Audience.ChatType == "":
		return errors.New("requester context audience is incomplete")
	}

	seenCapabilities := make(map[string]struct{}, len(ctx.Authorization.Capabilities))
	hasRequiredCapability := false
	for _, capability := range ctx.Authorization.Capabilities {
		if capability == "" {
			return errors.New("requester context contains an empty capability")
		}
		if _, duplicate := seenCapabilities[capability]; duplicate {
			return errors.New("requester context contains duplicate capabilities")
		}
		seenCapabilities[capability] = struct{}{}
		if capability == cfg.Capability {
			hasRequiredCapability = true
		}
	}
	if !hasRequiredCapability {
		return fmt.Errorf("required capability %q is missing", cfg.Capability)
	}
	return nil
}

func decodeQDMClaim(payload json.RawMessage) (qdmScopeClaim, error) {
	var claim qdmScopeClaim
	if err := decodeStrictJSON(payload, &claim); err != nil {
		return qdmScopeClaim{}, fmt.Errorf("decode qdm scope claim: %w", err)
	}
	if claim.SchemaVersion != 1 {
		return qdmScopeClaim{}, fmt.Errorf("unsupported qdm scope schema version %d", claim.SchemaVersion)
	}
	if err := validateStringSet("manageAreaIds", claim.ManageAreaIDs); err != nil {
		return qdmScopeClaim{}, err
	}
	if err := validateStringSet("dcManageAreaIds", claim.DCManageAreaIDs); err != nil {
		return qdmScopeClaim{}, err
	}
	if err := validateStringSet("categoryLevel1Ids", claim.CategoryLevel1IDs); err != nil {
		return qdmScopeClaim{}, err
	}
	if len(claim.ManageAreaIDs) == 0 && len(claim.DCManageAreaIDs) == 0 {
		return qdmScopeClaim{}, errors.New("qdm scope has no manage area")
	}
	if len(claim.CategoryLevel1IDs) == 0 {
		return qdmScopeClaim{}, errors.New("qdm scope has no category")
	}
	return claim, nil
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func validateStringSet(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("qdm scope %s contains an empty or untrimmed value", name)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("qdm scope %s contains duplicate values", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validPolicyRevision(revision string) bool {
	if !strings.HasPrefix(revision, "sha256:") {
		return false
	}
	digest := strings.TrimPrefix(revision, "sha256:")
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
