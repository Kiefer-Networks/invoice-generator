package paperless

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fixtures follow Paperless-ngx v3.1.3 TaskSerializerV10 and PaperlessTask.Status.
func TestPaperlessAPI10Tasks(t *testing.T) {
	for _, tc := range []struct {
		status, result, ids string
		want                int64
		problem             error
	}{
		{"pending", "null", "[]", 0, ErrPending}, {"started", "null", "[]", 0, ErrPending},
		{"success", `{"document_id":42}`, "[42]", 42, nil}, {"failure", `{"error_message":"secret-token"}`, "[]", 0, ErrRemoteFailed}, {"revoked", "null", "[]", 0, ErrRemoteFailed},
	} {
		t.Run(tc.status, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json; version=10" {
					t.Error("API version not negotiated", r.Header.Get("Accept"))
				}
				if r.URL.Query().Get("task_id") != "task-123" {
					t.Error(r.URL)
				}
				w.Header().Set("X-Api-Version", "10")
				_, _ = fmt.Fprintf(w, `{"count":1,"next":null,"previous":null,"results":[{"id":7,"task_id":"task-123","task_type":"consume_file","trigger_source":"api_upload","status":%q,"result_data":%s,"related_document_ids":%s,"acknowledged":false}]}`, tc.status, tc.result, tc.ids)
			}))
			defer s.Close()
			c, _ := NewClient(Config{URL: s.URL, APIKey: "secret-token"}, s.Client(), true)
			id, e := c.Poll(context.Background(), "task-123")
			if id != tc.want || !errors.Is(e, tc.problem) {
				t.Fatal(id, e)
			}
			if e != nil && strings.Contains(e.Error(), "secret-token") {
				t.Fatal(e)
			}
		})
	}
}
func TestPaperlessAPI10RejectsIncompatibleAndAmbiguousPages(t *testing.T) {
	for _, tc := range []struct {
		name, body, version string
		code                int
		problem             error
	}{
		{"unsupported", `{"detail":"secret-token"}`, "9", 406, ErrAPIIncompatible},
		{"wrong_version", `{"count":0,"next":null,"previous":null,"results":[]}`, "9", 200, ErrAPIIncompatible},
		{"legacy_array", `[{"task_id":"task-123","status":"SUCCESS","related_document":42}]`, "", 200, ErrAPIIncompatible},
		{"missing_task", `{"count":0,"next":null,"previous":null,"results":[]}`, "10", 200, ErrPending},
		{"foreign_next", `{"count":2,"next":"https://evil.test/token","previous":null,"results":[]}`, "10", 200, ErrResponse},
		{"missing_results", `{"count":0}`, "10", 200, ErrResponse},
		{"duplicate", `{"count":2,"next":null,"previous":null,"results":[{"task_id":"task-123"},{"task_id":"task-123"}]}`, "10", 200, ErrResponse},
		{"wrong_task", `{"count":1,"results":[{"task_id":"different","status":"success","related_document_ids":[42]}]}`, "10", 200, ErrResponse},
		{"conflicting_ids", `{"count":1,"results":[{"task_id":"task-123","task_type":"consume_file","trigger_source":"api_upload","status":"success","result_data":{"document_id":43},"related_document_ids":[42]}]}`, "10", 200, ErrResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("X-Api-Version", tc.version)
				w.WriteHeader(tc.code)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c, _ := NewClient(Config{URL: s.URL, APIKey: "secret-token"}, s.Client(), true)
			_, e := c.Poll(context.Background(), "task-123")
			if !errors.Is(e, tc.problem) || calls != 1 {
				t.Fatal(e, calls)
			}
			if strings.Contains(e.Error(), "secret-token") {
				t.Fatal(e)
			}
		})
	}
}

func TestPaperlessAPI10SubmissionAcceptedAndUnsupported(t *testing.T) {
	for _, status := range []int{200, 406} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json; version=10" {
					t.Error("unversioned upload")
				}
				w.Header().Set("X-Api-Version", "10")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = fmt.Fprint(w, `"9266c16d-3632-4810-b48d-7c9c5bd54e0e"`)
				} else {
					_, _ = fmt.Fprint(w, `{"detail":"secret-token"}`)
				}
			}))
			defer s.Close()
			c, _ := NewClient(Config{URL: s.URL, APIKey: "secret-token"}, s.Client(), true)
			task, e := c.Submit(context.Background(), strings.NewReader("%PDF"), 4, "title", nil)
			if status == 200 {
				if e != nil || task != "9266c16d-3632-4810-b48d-7c9c5bd54e0e" {
					t.Fatal(task, e)
				}
			} else {
				var failure *SubmissionError
				if !errors.As(e, &failure) || failure.Certainty != Rejected || !errors.Is(e, ErrAPIIncompatible) || strings.Contains(e.Error(), "secret-token") {
					t.Fatal(e)
				}
			}
		})
	}
}
