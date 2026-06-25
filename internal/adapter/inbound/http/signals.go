package http

import (
	"errors"

	"github.com/alkem-io/wopi-service/internal/domain/service"
)

// Outcome categories for the putfile and token_issuance health signals. These
// values are part of the alerting contract operators key on
// (specs/003-health-signals/contracts/log-signals.md). They live here because
// both handlers (same package) emit them and `internal` is shared between the
// two signals — one canonical location (Constitution VIII).
const (
	outcomeWriteFailed      = "write_failed"
	outcomeLockRepoError    = "lock_repo_error"
	outcomeMetadataLookup   = "metadata_lookup_failed"
	outcomeDiscoveryUnavail = "discovery_unavailable"
	outcomeTokenPersist     = "token_persist_failed"
	outcomeInternal         = "internal"
)

// putFileOutcome classifies a genuine PutFile failure into its outcome category
// via the domain sentinels. Anything unrecognized is an uncategorized internal
// error.
func putFileOutcome(err error) string {
	switch {
	case errors.Is(err, service.ErrFileWrite):
		return outcomeWriteFailed
	case errors.Is(err, service.ErrLockRepo):
		return outcomeLockRepoError
	default:
		return outcomeInternal
	}
}

// tokenIssuanceOutcome classifies a genuine token-issuance failure into its
// outcome category. Both discovery-outage sentinels (empty cache and cold fetch)
// map to discovery_unavailable so a single class selector matches a Collabora
// outage regardless of cache state.
func tokenIssuanceOutcome(err error) string {
	switch {
	case errors.Is(err, service.ErrNoDiscoveryData), errors.Is(err, service.ErrDiscoveryFetch):
		return outcomeDiscoveryUnavail
	case errors.Is(err, service.ErrDocumentLookup):
		return outcomeMetadataLookup
	case errors.Is(err, service.ErrTokenPersist):
		return outcomeTokenPersist
	default:
		return outcomeInternal
	}
}
