// Package obs defines the canonical structured-log field convention for the
// health-signal observability feature. These constants are the contract that
// operator alert expressions depend on (see specs/003-health-signals); they
// live here as the single source of truth because they are referenced across
// the http (inbound adapter) and service (domain) packages.
package obs

// Field keys shared by every health-signal record.
const (
	// FieldEvent tags which health signal a record belongs to (one of the
	// Event* values below).
	FieldEvent = "event"
	// FieldOutcome carries the low-cardinality failure category.
	FieldOutcome = "outcome"
	// FieldDocumentID correlates a record to a document/file.
	FieldDocumentID = "documentId"
	// FieldActorID correlates a token-issuance record to the issuing actor.
	FieldActorID = "actorId"
)

// Event names — the stable signal identifiers a single alert expression selects on.
const (
	// EventTokenIssuance marks genuine WOPI token-issuance failures.
	EventTokenIssuance = "token_issuance"
	// EventPutFile marks genuine document save (PutFile) failures.
	EventPutFile = "putfile"
	// EventCollaboraReachability marks Collabora reachability transitions.
	EventCollaboraReachability = "collabora_reachability"
)
