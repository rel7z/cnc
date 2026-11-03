// Package version contains the shared data contracts used by deep CMS version
// discovery, rule artifacts, and version-result transport.
package version

import "time"

const (
	// ArtifactSchemaVersionV1 is the schema version of a rule artifact.
	ArtifactSchemaVersionV1 = 1
	// ResultSchemaVersionV1 is the schema version of a version result record.
	ResultSchemaVersionV1 = 1

	// ConfidenceAuthoritative identifies a version selected from an authoritative
	// source or from an agreeing set whose highest-ranked source is authoritative.
	ConfidenceAuthoritative = "authoritative"
	// ConfidenceReported is a server-declared runtime version, not proof of patch state.
	ConfidenceReported = "reported"
	// ConfidenceValidatedFallback identifies a version selected from an approved
	// exact asset fallback source.
	ConfidenceValidatedFallback = "validated_fallback"
	// ConfidenceConflicted identifies evidence candidates that disagree and have
	// no matchable version.
	ConfidenceConflicted = "conflicted"
)

// VersionClause is one semantic-version comparison in a rule. Clauses in a
// VersionRule are evaluated as an AND set.
type VersionClause struct {
	Operator string `json:"operator"`
	Version  string `json:"version"`
}

// VersionRule is a named, component-targeted set of ANDed version clauses.
type VersionRule struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Component string          `json:"component"`
	Clauses   []VersionClause `json:"clauses"`
}

// EvidenceValidation contains only non-secret metadata about a validated
// evidence response.
type EvidenceValidation struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
}

// ValidationMetadata is retained as a descriptive alias for callers that use
// the metadata terminology from the transport contract.
type ValidationMetadata = EvidenceValidation

// EvidenceCandidate is one validated public-evidence version candidate.
type EvidenceCandidate struct {
	Source      string             `json:"source"`
	Rank        int                `json:"rank"`
	Version     string             `json:"version"`
	RetrievedAt time.Time          `json:"retrieved_at"`
	Validation  EvidenceValidation `json:"validation"`
}

// VersionObservation is the resolved version state for one CMS component.
// Version is nil when no matchable version is known, including conflicts.
type VersionObservation struct {
	Component          string              `json:"component"`
	Version            *string             `json:"version"`
	Confidence         string              `json:"confidence"`
	SelectedEvidence   *EvidenceCandidate  `json:"selected_evidence"`
	EvidenceCandidates []EvidenceCandidate `json:"evidence_candidates"`
}

// TargetLocator is the privacy-safe canonical locator retained in artifacts
// and version results. It intentionally has no query, fragment, userinfo, or
// request metadata fields.
type TargetLocator struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Path   string `json:"path"`
}

// TargetScope is the typed authorization scope bound to a rule artifact.
type TargetScope struct {
	Hosts   []string `json:"hosts"`
	CIDRs   []string `json:"cidrs"`
	Schemes []string `json:"schemes"`
	Ports   []int    `json:"ports"`
}

// AuthorizationScope is an alias for callers using the authorization-domain
// name for the same artifact field.
type AuthorizationScope = TargetScope

// ArtifactV1 is the immutable job-scoped rule artifact delivered to the
// scanner. Its digest and job binding are verified by later pipeline stages.
type ArtifactV1 struct {
	SchemaVersion int           `json:"schema_version"`
	JobID         string        `json:"job_id"`
	TargetScope   TargetScope   `json:"target_scope"`
	Rules         []VersionRule `json:"rules"`
}

// RuleArtifactV1 is a descriptive alias for ArtifactV1.
type RuleArtifactV1 = ArtifactV1

// VersionArtifactV1 is a descriptive alias for ArtifactV1.
type VersionArtifactV1 = ArtifactV1

// MatchedRule is the non-sensitive rule identity included in a result record.
type MatchedRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MatchedVersionRule is a descriptive alias for MatchedRule.
type MatchedVersionRule = MatchedRule

// ResultRecordV1 is one target/component version result in the versioned JSONL
// output contract.
type ResultRecordV1 struct {
	SchemaVersion int                `json:"schema_version"`
	JobID         string             `json:"job_id"`
	ShardID       string             `json:"shard_id"`
	Target        TargetLocator      `json:"target"`
	MatchedRules  []MatchedRule      `json:"matched_rules,omitempty"`
	Observation   VersionObservation `json:"observation"`
}

// VersionResultRecordV1 is a descriptive alias for ResultRecordV1.
type VersionResultRecordV1 = ResultRecordV1
