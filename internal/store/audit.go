package store

import (
	"context"
	"errors"
	"regexp"
)

var auditNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

// AuditEvent contains stable identifiers only. Human-entered values, error
// messages, credentials and request payloads must not cross this boundary.
type AuditEvent struct {
	ActorSubject string
	Action       string
	TargetType   string
	TargetID     string
	Result       string
	RequestID    string
}

// RecordAudit durably records an operation outcome without storing its payload.
func (s *Store) RecordAudit(ctx context.Context, event AuditEvent) error {
	if s == nil || s.db == nil {
		return errors.New("audit storage is unavailable")
	}
	if !auditNamePattern.MatchString(event.Action) || !auditNamePattern.MatchString(event.TargetType) || (event.Result != "success" && event.Result != "failure") {
		return errors.New("invalid audit event")
	}
	if len(event.ActorSubject) > 512 || len(event.TargetID) > 256 || len(event.RequestID) > 256 {
		return errors.New("audit identifier is too long")
	}
	id, err := newBusinessID()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_events(id,actor_subject,action,target_type,target_id,result,request_id,change_summary) VALUES(?,?,?,?,?,?,?,'{}')`, id, event.ActorSubject, event.Action, event.TargetType, event.TargetID, event.Result, event.RequestID)
	return err
}
