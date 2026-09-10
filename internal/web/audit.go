package web

import (
	"net/http"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func (a *app) audit(r *http.Request, actor, action, targetType, targetID, result string) error {
	if a.store == nil {
		return nil
	}
	requestID, _ := r.Context().Value(requestIDKey).(string)
	if actor == "" {
		if principal, ok := principalFromContext(r.Context()); ok {
			actor = principal.Subject
		}
	}
	return a.store.RecordAudit(r.Context(), store.AuditEvent{ActorSubject: actor, Action: action, TargetType: targetType, TargetID: targetID, Result: result, RequestID: requestID})
}

func mutationAuditEvent(r *http.Request, action, targetType string) store.AuditEvent {
	requestID, _ := r.Context().Value(requestIDKey).(string)
	actor := ""
	if principal, ok := principalFromContext(r.Context()); ok {
		actor = principal.Subject
	}
	return store.AuditEvent{ActorSubject: actor, Action: action, TargetType: targetType, Result: "success", RequestID: requestID}
}

func auditResult(err error) string {
	if err != nil {
		return "failure"
	}
	return "success"
}

func (a *app) auditMutation(w http.ResponseWriter, r *http.Request, action, targetType, targetID string, operationErr error) bool {
	if err := a.audit(r, "", action, targetType, targetID, auditResult(operationErr)); err != nil {
		a.logger.Error("persist mutation audit", "action", action, "target_type", targetType)
		http.Error(w, "unable to record operation", http.StatusInternalServerError)
		return false
	}
	return true
}

func actionForState(active bool, noun string) string {
	if active {
		return noun + ".restored"
	}
	return noun + ".archived"
}
