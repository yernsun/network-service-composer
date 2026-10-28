package nodecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func replaceReportFixtureDocument(t *testing.T, s Reports, key, kind, parent string, payload []byte) {
	t.Helper()
	before, err := s.Store.GetControlDocument(t.Context(), key)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	if err == nil && (before.Kind != kind || before.ParentID != parent) {
		// Deliberately model corrupted persisted metadata. Normal control
		// writes prevent changing identity, which is separately enforced here.
		sqlStore := s.Store.(*storage.SQLStore)
		if _, err = sqlStore.DB().ExecContext(t.Context(), "UPDATE control_documents SET kind=?, parent_id=?, payload=? WHERE id=?", kind, parent, payload, key); err != nil {
			t.Fatal(err)
		}
		return
	}
	now := time.Now().UTC()
	if err = s.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Expected: before.Revision, Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: payload, CreatedAt: now, UpdatedAt: now}}}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeSessionAdmissionNeverReclaimsInvalidOrExhaustedIdentity(t *testing.T) {
	for _, scenario := range []string{"empty boot", "oversized boot", "invalid owner", "missing ownership checker", "foreign binding", "corrupted session", "foreign session kind", "foreign session parent", "exhausted generation"} {
		t.Run(scenario, func(t *testing.T) {
			reports, _, owner := reportsFixture(t)
			boot, actualOwner := "boot", *owner
			switch scenario {
			case "empty boot":
				boot = ""
			case "oversized boot":
				boot = strings.Repeat("b", 129)
			case "invalid owner":
				actualOwner = 0
			case "missing ownership checker":
				reports.CheckOwner = nil
			case "foreign binding":
				b := reports.Bindings["controller-a"]
				b.InstanceID = "another"
				reports.Bindings["controller-a"] = b
			default:
				kind, parent, payload := "node-session", "node-a", []byte(`{"generation":1,"owner":11,"boot_id":"old"}`)
				switch scenario {
				case "corrupted session":
					payload = []byte(`{`)
				case "foreign session kind":
					kind = "foreign"
				case "foreign session parent":
					parent = "node-b"
				case "exhausted generation":
					payload, _ = json.Marshal(Session{Generation: math.MaxInt64, Owner: 11, BootID: "old"})
				}
				replaceReportFixtureDocument(t, reports, sessionKey("node-a"), kind, parent, payload)
			}
			before, beforeErr := reports.Store.GetControlDocument(t.Context(), sessionKey("node-a"))
			if _, err := reports.Begin(t.Context(), "controller-a", "node-a", "engine-a", boot, actualOwner); err == nil {
				t.Fatal("invalid session reclaimed identity")
			}
			after, afterErr := reports.Store.GetControlDocument(t.Context(), sessionKey("node-a"))
			if !errors.Is(afterErr, beforeErr) || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
				t.Fatal("rejected begin altered durable generation", afterErr)
			}
		})
	}
}

func TestNodeReportTamperingCannotBecomeCurrentInventory(t *testing.T) {
	for _, scenario := range []string{"foreign report kind", "foreign report parent", "broken envelope", "ciphertext tampered", "ciphertext wrong document", "broken plaintext", "foreign node plaintext", "sequence mismatch", "generation mismatch", "future plaintext"} {
		t.Run(scenario, func(t *testing.T) {
			s, now, owner := reportsFixture(t)
			session, err := s.Begin(t.Context(), "controller-a", "node-a", "engine-a", "boot", *owner)
			if err != nil {
				t.Fatal(err)
			}
			value := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
			if err = s.Accept(t.Context(), "controller-a", value); err != nil {
				t.Fatal(err)
			}
			doc, err := s.Store.GetControlDocument(t.Context(), reportKey("node-a"))
			if err != nil {
				t.Fatal(err)
			}
			kind, parent, payload := doc.Kind, doc.ParentID, doc.Payload
			switch scenario {
			case "foreign report kind":
				kind = "foreign"
			case "foreign report parent":
				parent = "node-b"
			case "broken envelope":
				payload = []byte(`{`)
			default:
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				aad := "control:" + doc.ID
				switch scenario {
				case "ciphertext tampered":
					payload = []byte(`{"ciphertext":"AAAA"}`)
				case "ciphertext wrong document":
					aad = "control:node-report:node-b"
				case "broken plaintext":
					raw = []byte(`{`)
				case "foreign node plaintext":
					value.NodeID = "node-b"
					raw, _ = json.Marshal(value)
				case "sequence mismatch":
					value.Sequence = 2
					raw, _ = json.Marshal(value)
				case "generation mismatch":
					value.Generation = 2
					raw, _ = json.Marshal(value)
				case "future plaintext":
					value.Observation.At = now.Add(time.Minute)
					raw, _ = json.Marshal(value)
				}
				if scenario != "ciphertext tampered" {
					encrypted, err := s.Keys.Encrypt(raw, []byte(aad))
					if err != nil {
						t.Fatal(err)
					}
					payload, err = json.Marshal(map[string]any{"ciphertext": encrypted})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			replaceReportFixtureDocument(t, s, doc.ID, kind, parent, payload)
			if _, err = s.Read(t.Context(), "node-a"); err == nil {
				t.Fatal("tampered inventory trusted")
			}
		})
	}
}

func TestReportRejectsLostOwnerAndForeignClusterWithoutAdvancingSession(t *testing.T) {
	for _, scenario := range []string{"owner lost", "ownership checker removed", "foreign cluster", "foreign report document"} {
		t.Run(scenario, func(t *testing.T) {
			s, now, owner := reportsFixture(t)
			session, err := s.Begin(t.Context(), "controller-a", "node-a", "engine-a", "boot", *owner)
			if err != nil {
				t.Fatal(err)
			}
			value := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
			switch scenario {
			case "owner lost":
				*owner = 12
			case "ownership checker removed":
				s.CheckOwner = nil
			case "foreign cluster":
				b := s.Bindings["controller-a"]
				b.ClusterID = "cluster-a"
				s.Bindings["controller-a"] = b
				value.Observation.Info.Swarm.Cluster = &swarm.ClusterInfo{ID: "cluster-b"}
			case "foreign report document":
				replaceReportFixtureDocument(t, s, reportKey("node-a"), "foreign", "node-a", []byte(`{}`))
			}
			if err = s.Accept(t.Context(), "controller-a", value); err == nil {
				t.Fatal("unowned report accepted")
			}
			got, _, err := s.session(context.Background(), "node-a")
			if err != nil || got.Sequence != 0 || got.Generation != session.Generation {
				t.Fatal("rejected report consumed sequence", got, err)
			}
		})
	}
}
