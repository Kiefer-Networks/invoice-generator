package web

import (
	"strings"
	"testing"
)

func TestRoutineActionsUseAccessibleIconControls(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"templates/layout.html": {
			`aria-label="Browse invoices" title="Browse invoices">▤</a>`,
		},
		"templates/customers.html": {
			`aria-label="More customers" title="More customers">→</a>`,
		},
		"templates/customer_detail.html": {
			`aria-label="Edit customer" title="Edit customer">✎</a>`,
			`aria-label="Archive customer" title="Archive customer">⌑</button>`,
			`aria-label="Restore customer" title="Restore customer">↻</button>`,
		},
		"templates/catalog.html": {
			`aria-label="More catalog items" title="More catalog items">→</a>`,
		},
		"templates/catalog_detail.html": {
			`aria-label="Edit catalog item" title="Edit catalog item">✎</a>`,
			`aria-label="Archive catalog item" title="Archive catalog item">⌑</button>`,
			`aria-label="Restore catalog item" title="Restore catalog item">↻</button>`,
		},
		"templates/invoice_items.html": {
			`aria-label="Move {{.Title}} up" title="Move {{.Title}} up">↑</button>`,
			`aria-label="Move {{.Title}} down" title="Move {{.Title}} down">↓</button>`,
			`aria-label="Edit position" title="Edit position">✎</summary>`,
			`aria-label="Remove position" title="Remove position">×</button>`,
		},
		"templates/invoice_detail.html": {
			`aria-label="Preview PDF" title="Preview PDF">◉</a>`,
			`aria-label="Download PDF with ZUGFeRD" title="Download PDF with ZUGFeRD">⇩</a>`,
			`aria-label="Back to invoices" title="Back to invoices">←</a>`,
		},
		"templates/invoices.html": {
			`aria-label="Search invoices" title="Search invoices">⌕</button>`,
			`aria-label="More invoices" title="More invoices">→</a>`,
			`aria-label="Search customers" title="Search customers">⌕</button>`,
			`aria-label="More customers" title="More customers">→</a>`,
			`aria-label="Search catalog" title="Search catalog">⌕</button>`,
			`aria-label="More catalog items" title="More catalog items">→</a>`,
		},
	}

	for name, snippets := range want {
		name, snippets := name, snippets
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body, err := embeddedFiles.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, snippet := range snippets {
				if !strings.Contains(string(body), snippet) {
					t.Errorf("missing accessible icon control %q", snippet)
				}
			}
		})
	}

	css, err := embeddedFiles.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{".icon-button", ".icon-action", "min-width:44px", "min-height:44px"} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("icon control CSS is missing %q", rule)
		}
	}
}
