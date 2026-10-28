package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIssueQueriesFilterPersistedOwnershipAcrossPages(t *testing.T) {
	c, _, _ := controlFixture(t)
	batch := []storage.ControlWrite{}
	now := c.now()
	for i := 0; i < 205; i++ {
		v := Issue{Code: "runtime_unavailable", ResourceType: "runtime", ResourceID: "shared", Active: i%2 == 0, ObservationState: "UNKNOWN", Scopes: []AlertScope{{ProjectID: "team", EnvironmentID: "env"}}, FirstSeenAt: now, LastSeenAt: now}
		if i == 204 {
			v.Scopes = append(v.Scopes, AlertScope{ProjectID: "other", EnvironmentID: "other-env"})
		}
		w, err := c.controlWrite(fmt.Sprintf("issue_%03d", i), "issue", "runtime", v, 0)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, w)
	}
	if err := c.store.WriteControlDocuments(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filter IssueFilter
		count  int
	}{
		{IssueFilter{}, 200}, {IssueFilter{State: "all"}, 200}, {IssueFilter{State: "active"}, 103}, {IssueFilter{State: "resolved"}, 102},
		{IssueFilter{ResourceID: "foreign"}, 0}, {IssueFilter{ResourceType: "certificate"}, 0}, {IssueFilter{Code: "other"}, 0}, {IssueFilter{ProjectID: "foreign"}, 0},
		{IssueFilter{ProjectID: "team", EnvironmentID: "other-env"}, 0}, {IssueFilter{ProjectID: "other", EnvironmentID: "other-env"}, 1},
	} {
		rows, err := c.IssuesFiltered(t.Context(), storage.Page{Limit: 500}, tc.filter)
		if err != nil || len(rows) != tc.count {
			t.Fatal(tc.filter, len(rows), err)
		}
	}
	rows, err := c.IssuesFiltered(t.Context(), storage.Page{Limit: 1, After: "issue_203"}, IssueFilter{State: "active"})
	if err != nil || len(rows) != 1 || rows[0].ID != "issue_204" {
		t.Fatal(rows, err)
	}
	if _, err = c.IssuesFiltered(t.Context(), storage.Page{}, IssueFilter{State: "forged"}); err == nil {
		t.Fatal("invalid state accepted")
	}
	if err = c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: "issue_bad", Kind: "issue", Payload: []byte("{")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.IssuesFiltered(t.Context(), storage.Page{After: "issue_204"}, IssueFilter{}); err == nil {
		t.Fatal("unreadable issue became an empty success")
	}
}

func TestCertificateRecoveryRequiresTheCompleteCurrentValidityWindow(t *testing.T) {
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	expiry := now.Add(90 * 24 * time.Hour)
	cert := model.Certificate{Status: model.CertificateActive, NotBefore: &past, NotAfter: &expiry}
	if bad, known := certificateCondition(cert, now); bad || !known {
		t.Fatal("valid window not confirmed")
	}
	cert.NotBefore = &future
	if bad, known := certificateCondition(cert, now); !bad || !known {
		t.Fatal("future certificate healed a problem")
	}
	cert.NotBefore = nil
	if bad, known := certificateCondition(cert, now); bad || known {
		t.Fatal("incomplete window healed a problem")
	}
	cert.NotBefore = &past
	cert.Status = model.CertificateRenewing
	if _, known := certificateCondition(cert, now); known {
		t.Fatal("renewal in progress became confirmed success")
	}
}

func TestNotificationCollectionAgeAndUnicodeLimits(t *testing.T) {
	c, _ := routedFixture(t)
	now := c.now()
	c.now = func() time.Time { return now }
	at := now
	for i, tc := range []struct {
		age    time.Duration
		failed bool
		want   string
	}{{0, false, "CURRENT"}, {-3 * time.Minute, false, "UNKNOWN"}, {time.Minute, false, "UNKNOWN"}, {0, true, "FAILED"}} {
		at = now.Add(tc.age)
		if err := c.saveNotificationHealth(t.Context(), fmt.Sprintf("health_%d", i), notificationCollectionHealth{Group: fmt.Sprint(i), AttemptedAt: at, SucceededAt: &at, Failed: tc.failed}, 0); err != nil {
			t.Fatal(err)
		}
		value, err := c.NotificationHealth(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, source := range value.Sources {
			if source.Group == fmt.Sprint(i) {
				found = true
				if source.State != tc.want {
					t.Fatal(source)
				}
			}
		}
		if !found {
			t.Fatal("source health disappeared")
		}
	}
	receiver := routedReceiver(t, c, "receiver_unicode")
	for _, n := range []int{100, 101} {
		receiver.Name = ""
		for i := 0; i < n; i++ {
			receiver.Name += "名"
		}
		_, err := c.SaveNotificationReceiver(t.Context(), receiver.ID, NotificationReceiverInput{Name: receiver.Name, Enabled: true}, receiver.Revision)
		if (err == nil) != (n == 100) {
			t.Fatal(n, err)
		}
		if err == nil {
			receiver.Revision++
		}
	}
	for _, n := range []int{512, 513} {
		reason := ""
		for i := 0; i < n; i++ {
			reason += "维"
		}
		_, err := c.SaveNotificationSilence(t.Context(), fmt.Sprintf("silence_unicode%d", n), NotificationSilenceInput{ScopeType: "global", Reason: reason}, 0, "admin")
		if (err == nil) != (n == 512) {
			t.Fatal(n, err)
		}
	}
}

type issueReadFailure struct{ storage.Store }

func (issueReadFailure) ListControlDocuments(context.Context, string, string, storage.Page) ([]storage.ControlDocument, error) {
	return nil, errors.New("store unavailable")
}
func TestIssueQueryFailurePreservesUnknown(t *testing.T) {
	c, _, _ := controlFixture(t)
	c.store = issueReadFailure{c.store}
	if _, err := c.IssuesFiltered(t.Context(), storage.Page{}, IssueFilter{}); err == nil {
		t.Fatal("failed scan reported no issues")
	}
}

func TestNotificationHistoryResourceFilterUsesStoredEvent(t *testing.T) {
	c, _ := routedFixture(t)
	for _, resource := range []string{"target", "foreign"} {
		v := Notification{Event: NotificationEvent{ID: resource, ResourceID: resource, Type: "runtime.problem"}, Status: "DELIVERED"}
		if err := c.saveControl(t.Context(), "notice_"+resource, notificationDeliveryKind, "", v, 0); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := c.ListNotificationsFiltered(t.Context(), storage.Page{}, NotificationFilter{ResourceID: "target"})
	if err != nil || len(rows) != 1 || rows[0].Event.ResourceID != "target" {
		t.Fatal(rows, err)
	}
}

func TestGatewayRecoveryRejectsFutureAndExpiredObservationEvenWithFreshTransport(t *testing.T) {
	c, release := releaseFixture(t, 1)
	now := c.now()
	c.now = func() time.Time { return now }
	g, err := c.store.GetGateway(t.Context(), release.Targets[0].GatewayID)
	if err != nil {
		t.Fatal(err)
	}
	g.Status = model.GatewayReady
	g.LastSeenAt = &now
	if err = c.store.UpdateGateway(t.Context(), &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	evidence := GatewayEvidence{ObservedAt: now, ReceivedAt: now, RefreshIntervalMillis: 1000, LoadingConfirmed: true, ConfirmedRevision: g.DesiredRevision, StartupState: "CONFIRMED"}
	for _, age := range []time.Duration{-4 * time.Second, 10 * time.Second, 0} {
		evidence.ObservedAt = now.Add(age)
		if err = c.saveControl(t.Context(), g.ID+":evidence", "gateway-evidence", g.ID, evidence, 0); err != nil {
			doc, e := c.store.GetControlDocument(t.Context(), g.ID+":evidence")
			if e != nil {
				t.Fatal(e)
			}
			if e = c.saveControl(t.Context(), g.ID+":evidence", "gateway-evidence", g.ID, evidence, doc.Revision); e != nil {
				t.Fatal(e)
			}
		}
		actual, err := c.GatewayEvidence(t.Context(), g.ID)
		if err != nil || (actual.ConfigurationState == "CONFIRMED") != (age == 0) || actual.LoadingConfirmed != (age == 0) {
			t.Fatal(age, actual, err)
		}
	}
}

func TestCertificateIssueRequiresACompleteCurrentWindowBeforeRecovery(t *testing.T) {
	c, rt, _ := controlFixture(t)
	now := c.now()
	c.now = func() time.Time { return now }
	expiry := now.Add(90 * 24 * time.Hour)
	cert := model.Certificate{ID: "window-incident", PoolID: rt.PoolID, Provider: "acme", Status: model.CertificateError, NotAfter: &expiry, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.CreateCertificate(t.Context(), &cert); err != nil {
		t.Fatal(err)
	}
	observe := func() Issue {
		t.Helper()
		if err := c.RefreshCommunityIssues(t.Context()); err != nil {
			t.Fatal(err)
		}
		rows, err := c.IssuesFiltered(t.Context(), storage.Page{}, IssueFilter{ResourceID: cert.ID})
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	initial := observe()
	if !initial.Active {
		t.Fatal(initial)
	}
	acknowledged, err := c.AcknowledgeIssue(t.Context(), initial.ID, initial.Revision, "operator")
	if err != nil || !acknowledged.Active {
		t.Fatal(acknowledged, err)
	}
	cert.Status = model.CertificateActive
	if err = c.store.UpdateCertificate(t.Context(), &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	cert.Revision++
	unknown := observe()
	if !unknown.Active || unknown.ResolvedAt != nil || !unknown.Acknowledged {
		t.Fatal("missing validity healed issue", unknown)
	}
	future := now.Add(time.Hour)
	cert.NotBefore = &future
	if err = c.store.UpdateCertificate(t.Context(), &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	cert.Revision++
	if issue := observe(); !issue.Active || issue.ResolvedAt != nil {
		t.Fatal("future validity healed issue", issue)
	}
	past := now.Add(-time.Hour)
	cert.NotBefore = &past
	if err = c.store.UpdateCertificate(t.Context(), &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	recovered := observe()
	if recovered.Active || recovered.ResolvedAt == nil || recovered.ID != initial.ID || recovered.Occurrences != 1 {
		t.Fatal("complete valid certificate did not recover the original occurrence", recovered)
	}
}
