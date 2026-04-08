package port

import "context"

// DiscoveryData holds parsed WOPI discovery information from Collabora.
type DiscoveryData struct {
	Actions  []DiscoveryAction
	ProofKey ProofKey
}

// DiscoveryAction represents a single editor action from WOPI discovery.
type DiscoveryAction struct {
	App    string
	Name   string
	Ext    string
	URLSrc string
}

// ProofKey holds RSA public key data for WOPI proof validation.
type ProofKey struct {
	Modulus     string
	Exponent    string
	OldModulus  string
	OldExponent string
}

// DiscoveryClient fetches WOPI discovery data from Collabora.
type DiscoveryClient interface {
	// FetchDiscovery retrieves and parses the discovery XML.
	FetchDiscovery(ctx context.Context) (*DiscoveryData, error)
}
