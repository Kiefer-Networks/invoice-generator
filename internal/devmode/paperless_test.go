//go:build !production

package devmode

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/paperless"
)

func TestDevPaperlessRejectsOversizedMultipartBody(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("document", "invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(strings.Repeat("x", 21<<20))); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/documents/post_document/", &body)
	r.Header.Set("Authorization", "Token "+PaperlessToken)
	r.Header.Set("Content-Type", form.FormDataContentType())
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	p := &Paperless{state: "accepted"}
	w := httptest.NewRecorder()
	p.serve(w, r)
	if w.Code != http.StatusBadRequest || len(p.docs) != 0 {
		t.Fatalf("oversized body returned status %d and created %d documents", w.Code, len(p.docs))
	}
}

func TestDevPaperlessStates(t *testing.T) {
	for _, state := range []string{"accepted", "delayed", "rejected", "timeout"} {
		t.Run(state, func(t *testing.T) {
			p, e := StartPaperless(state)
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			c, e := paperless.NewClient(paperless.Config{URL: p.URL, APIKey: PaperlessToken}, &http.Client{Timeout: 100 * time.Millisecond}, true)
			if e != nil {
				t.Fatal(e)
			}
			task, e := c.Submit(context.Background(), bytes.NewBufferString("PDF"), 3, "Test", nil)
			switch state {
			case "rejected":
				if !errors.Is(e, paperless.ErrRejected) {
					t.Fatal(e)
				}
			case "timeout":
				if e == nil {
					t.Fatal("timeout accepted")
				}
			default:
				if e != nil {
					t.Fatal(e)
				}
				id, e := c.Poll(context.Background(), task)
				if state == "delayed" {
					if !errors.Is(e, paperless.ErrPending) {
						t.Fatal(e)
					}
				} else if e != nil || id != 1 {
					t.Fatalf("poll %d %v", id, e)
				}
			}
		})
	}
}
