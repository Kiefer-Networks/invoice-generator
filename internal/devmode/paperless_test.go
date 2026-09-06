package devmode

import (
	"bytes"
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"net/http"
	"testing"
	"time"
)

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
