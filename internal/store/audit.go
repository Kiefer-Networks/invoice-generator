package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

var auditNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

// ErrAudit identifies a failed audit write for a mutation that was rolled back.
var ErrAudit = errors.New("audit write failed")

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
	return recordAudit(ctx, s.db, event)
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func recordAudit(ctx context.Context, executor auditExecutor, event AuditEvent) error {
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
	_, err = executor.ExecContext(ctx, `INSERT INTO audit_events(id,actor_subject,action,target_type,target_id,result,request_id,change_summary) VALUES(?,?,?,?,?,?,?,'{}')`, id, event.ActorSubject, event.Action, event.TargetType, event.TargetID, event.Result, event.RequestID)
	return err
}

func auditedMutation[T any](ctx context.Context, s *Store, event AuditEvent, mutate func(*sql.Tx) (T, string, error)) (result T, err error) {
	if s == nil || s.db == nil {
		return result, errors.New("audit storage is unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin audited mutation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, event.TargetID, err = mutate(tx)
	if err != nil {
		var zero T
		return zero, err
	}
	event.Result = "success"
	if err = recordAudit(ctx, tx, event); err != nil {
		var zero T
		return zero, fmt.Errorf("%w: %v", ErrAudit, err)
	}
	if err = tx.Commit(); err != nil {
		var zero T
		return zero, fmt.Errorf("commit audited mutation: %w", err)
	}
	return result, nil
}
