package port

import "context"

// AuthResult holds the result of an authorization check.
type AuthResult struct {
	Allowed bool
	Reason  string
}

// AuthService checks authorization via the auth-evaluation-service.
type AuthService interface {
	// CheckPrivilege verifies whether an agent has a privilege on a resource.
	CheckPrivilege(ctx context.Context, actorID, privilege, authorizationPolicyID string) (*AuthResult, error)
}
