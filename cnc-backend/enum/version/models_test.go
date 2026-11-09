package version

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestArtifactV1JSONShape(t *testing.T) {
	artifact := ArtifactV1{
		SchemaVersion: ArtifactSchemaVersionV1,
		JobID:         "job_123",
		TargetScope: TargetScope{
			Hosts:   []string{"owned-fixtures.example.test"},
			CIDRs:   []string{"192.0.2.0/24"},
			Schemes: []string{"https"},
			Ports:   []int{443},
		},
		Rules: []VersionRule{{
			ID:        "vr_01",
			Name:      "Joomla 5.1 series",
			Component: "joomla:core",
			Clauses: []VersionClause{
				{Operator: ">=", Version: "5.1.0"},
				{Operator: "<", Version: "5.2.0"},
			},
		}},
	}

	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}

	var gotValue, wantValue any
	want := `{"schema_version":1,"job_id":"job_123","target_scope":{"hosts":["owned-fixtures.example.test"],"cidrs":["192.0.2.0/24"],"schemes":["https"],"ports":[443]},"rules":[{"id":"vr_01","name":"Joomla 5.1 series","component":"joomla:core","clauses":[{"operator":">=","version":"5.1.0"},{"operator":"<","version":"5.2.0"}]}]}`
	if err := json.Unmarshal(data, &gotValue); err != nil {
		t.Fatalf("decode artifact JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected artifact JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("unexpected artifact JSON:\n got %s\nwant %s", data, want)
	}
}

func TestResultRecordV1JSONShape(t *testing.T) {
	version := "5.1.3"
	retrievedAt := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
	candidate := EvidenceCandidate{
		Source:      "joomla_manifest:core:10",
		Rank:        10,
		Version:     version,
		RetrievedAt: retrievedAt,
		Validation: EvidenceValidation{
			Kind:        "xml_version",
			ContentType: "application/xml",
			Bytes:       1234,
		},
	}
	record := ResultRecordV1{
		SchemaVersion: ResultSchemaVersionV1,
		JobID:         "job_123",
		ShardID:       "shard_02",
		Target: TargetLocator{
			Scheme: "https",
			Host:   "owned-fixtures.example.test",
			Port:   443,
			Path:   "/",
		},
		MatchedRules: []MatchedRule{{ID: "vr_01", Name: "Joomla 5.1 series"}},
		Observation: VersionObservation{
			Component:          "joomla:core",
			Version:            &version,
			Confidence:         ConfidenceAuthoritative,
			SelectedEvidence:   &candidate,
			EvidenceCandidates: []EvidenceCandidate{candidate},
		},
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal result record: %v", err)
	}

	for _, field := range []string{
		`"schema_version":1`,
		`"target":{"scheme":"https","host":"owned-fixtures.example.test","port":443,"path":"/"}`,
		`"matched_rules":[{"id":"vr_01","name":"Joomla 5.1 series"}]`,
		`"confidence":"authoritative"`,
		`"retrieved_at":"2026-10-09T00:00:00Z"`,
		`"validation":{"kind":"xml_version","content_type":"application/xml","bytes":1234}`,
	} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("result JSON missing %s: %s", field, data)
		}
	}
}

func TestVersionObservationConflictEncodesNullVersion(t *testing.T) {
	data, err := json.Marshal(VersionObservation{
		Component:          "joomla:core",
		Confidence:         ConfidenceConflicted,
		EvidenceCandidates: []EvidenceCandidate{},
	})
	if err != nil {
		t.Fatalf("marshal conflicted observation: %v", err)
	}
	if string(data) != `{"component":"joomla:core","version":null,"confidence":"conflicted","selected_evidence":null,"evidence_candidates":[]}` {
		t.Fatalf("unexpected conflicted observation JSON: %s", data)
	}
}
