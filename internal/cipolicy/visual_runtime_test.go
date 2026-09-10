package cipolicy

import (
	"os"
	"strings"
	"testing"
)

func TestVisualRegressionInheritsProductionRuntimeAndFonts(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	content := string(dockerfile)
	for _, required := range []string{
		"COPY docker/visual-fonts.conf /etc/fonts/local.conf",
		"fc-match system-ui",
		"FROM runtime AS visual",
		"poppler-utils=25.12.0-r1",
		"INVOICE_VISUAL_RUNTIME=alpine-3.24.1-chromium-152",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Dockerfile must contain %q", required)
		}
	}

	script, err := os.ReadFile("../../scripts/test-visual.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "--target visual") || strings.Contains(string(script), "docker/visual.Dockerfile") {
		t.Fatal("visual regression must build the visual stage from the production Dockerfile")
	}
}
