package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRecordAuditPersistsOnlyStableIdentifiers(t *testing.T) {
	t.Parallel()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := AuditEvent{ActorSubject: "subject-ada", Action: "customer.created", TargetType: "customer", TargetID: "customer-id", Result: "success", RequestID: "request-id"}
	if err = s.RecordAudit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var actor, action, targetType, targetID, result, requestID, summary string
	err = s.DB().QueryRow(`SELECT actor_subject,action,target_type,target_id,result,request_id,change_summary FROM audit_events`).Scan(&actor, &action, &targetType, &targetID, &result, &requestID, &summary)
	if err != nil {
		t.Fatal(err)
	}
	if actor != event.ActorSubject || action != event.Action || targetType != event.TargetType || targetID != event.TargetID || result != event.Result || requestID != event.RequestID || summary != "{}" {
		t.Fatalf("unexpected audit event: %q %q %q %q %q %q %q", actor, action, targetType, targetID, result, requestID, summary)
	}
}

func TestRecordAuditRejectsUnboundedOrInvalidIdentifiers(t *testing.T) {
	t.Parallel()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, event := range []AuditEvent{
		{Action: "", TargetType: "auth", Result: "success"},
		{Action: "auth.callback", TargetType: "auth", Result: "unknown"},
		{Action: "auth callback", TargetType: "auth", Result: "failure"},
	} {
		if err := s.RecordAudit(context.Background(), event); err == nil {
			t.Fatalf("accepted invalid event: %+v", event)
		}
	}
}
