package render

import (
	"context"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotRendererCannotFetchNetworkAssets(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	}))
	defer remote.Close()
	if err := FromSnapshot(context.Background(), &TplData{LogoPath: remote.URL + "/logo.svg"}, filepath.Join(t.TempDir(), "invoice.pdf")); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("snapshot renderer fetched a network asset")
	}
}

func TestSnapshotPolicyBlocksDefaultPortHTTPS(t *testing.T) {
	allocator, stop := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(FindChrome()))...)
	defer stop()
	ctx, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	blocked := make(chan string, 1)
	chromedp.ListenTarget(ctx, func(event any) {
		if e, ok := event.(*network.EventLoadingFailed); ok {
			select {
			case blocked <- e.ErrorText + " " + string(e.BlockedReason):
			default:
			}
		}
	})
	if err := chromedp.Run(ctx, network.Enable(), network.SetBlockedURLs().WithURLPatterns(snapshotNetworkPatterns()), chromedp.Navigate(`data:text/html,<img src="https://example.invalid/readiness.png">`)); err != nil {
		t.Fatal(err)
	}
	select {
	case yes := <-blocked:
		if !strings.Contains(yes, "BLOCKED_BY") && !strings.Contains(yes, "inspector") {
			t.Fatal("default-port HTTPS failed without being blocked by policy: " + yes)
		}
	case <-ctx.Done():
		t.Fatal("no network policy denial observed")
	}
}
