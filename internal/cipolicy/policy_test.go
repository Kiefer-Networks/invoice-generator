package cipolicy

import (
	"os"
	"strings"
	"testing"
)

func TestActionPins(t *testing.T) {
	for _, tc := range []struct {
		line  string
		valid bool
	}{
		{"- uses: actions/checkout@v7", false},
		{"- uses: actions/checkout@main", false},
		{"- uses: actions/checkout@0123456789012345678901234567890123456789 # v7.0.0", true},
		{"uses: './local/action'", true},
		{"uses: docker://alpine:3", false},
		{"uses: actions/checkout@0123456 # v7", false},
		{"uses: actions/checkout@0123456789012345678901234567890123456789", false},
		{"steps: [{uses: actions/checkout@main}]", false},
		{"'uses': actions/checkout@main", false},
	} {
		if err := ActionPins([]byte(tc.line)); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.line, err)
		}
	}
}

func TestContainerPolicy(t *testing.T) {
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if err := Container(compose, dockerfile); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, from, to string }{
		{"root", `user: "65532:65532"`, `user: "0:0"`},
		{"writable-root", "read_only: true", "read_only: false"},
		{"capability", "cap_drop: [ALL]", "cap_drop: [ALL]\n    cap_add: [SYS_ADMIN]"},
		{"public-port", "127.0.0.1:8080:8080", "0.0.0.0:8080:8080"},
		{"unbounded-tmp", ",size=536870912", ""},
		{"privileged", "init: true", "init: true\n    privileged: true"},
		{"host-network", "init: true", "init: true\n    network_mode: host"},
		{"missing-nnp", "no-new-privileges:true", "seccomp:unconfined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(compose), tc.from, tc.to, 1)
			if changed == string(compose) {
				t.Fatal("fixture not mutated")
			}
			if Container([]byte(changed), dockerfile) == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	if Container(compose, []byte(strings.Replace(string(dockerfile), "HEALTHCHECK --", "# HEALTHCHECK --", 1))) == nil {
		t.Fatal("missing healthcheck accepted")
	}
	if Container(compose, []byte(strings.Replace(string(dockerfile), "USER 65532:65532", "USER root", 1))) == nil {
		t.Fatal("root image accepted")
	}
	for _, changed := range []string{
		strings.Replace(string(dockerfile), "FROM runtime AS production", "FROM debian:trixie AS production", 1),
		string(dockerfile) + "\nUSER root\n",
		string(dockerfile) + "\nHEALTHCHECK NONE\n",
	} {
		if Container(compose, []byte(changed)) == nil {
			t.Fatal("unsafe effective production stage accepted")
		}
	}
}

func TestRepositoryPolicies(t *testing.T) {
	if err := Workflows("../../.github/workflows"); err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("../../scripts/install-ci-tool.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildxInstaller(installer); err != nil {
		t.Fatal(err)
	}
}

func TestBuildxMustBeVerifiedBeforeUse(t *testing.T) {
	install := map[string]any{"run": "bash scripts/install-ci-tool.sh buildx"}
	use := map[string]any{"run": "docker buildx create --use"}
	for _, tc := range []struct {
		name  string
		steps []map[string]any
		valid bool
	}{
		{"verified", []map[string]any{install, use}, true},
		{"runner-binary", []map[string]any{use}, false},
		{"installed-too-late", []map[string]any{use, install}, false},
		{"action-download", []map[string]any{{"uses": "docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e"}}, false},
		{"conditional-install", []map[string]any{{"run": "bash scripts/install-ci-tool.sh buildx", "if": "false"}, use}, false},
		{"ignored-install-failure", []map[string]any{{"run": "bash scripts/install-ci-tool.sh buildx", "continue-on-error": true}, use}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := BuildxSteps(tc.steps); (err == nil) != tc.valid {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestBuildxChecksumCannotBeRemoved(t *testing.T) {
	installer, err := os.ReadFile("../../scripts/install-ci-tool.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"ae43fa08c796b44efc86d7a63c55f73f7c35f3101188dea7bf93bcd6f99577ba", "sha256sum --check --status"} {
		changed := strings.Replace(string(installer), from, "removed", 1)
		if changed == string(installer) {
			t.Fatal("fixture not mutated")
		}
		if BuildxInstaller([]byte(changed)) == nil {
			t.Fatal("unverified installer accepted")
		}
	}
}

func TestRuntimeFreshnessPolicy(t *testing.T) {
	script, err := os.ReadFile("../../scripts/check-runtime-fresh.py")
	if err != nil {
		t.Fatal(err)
	}
	if err := RuntimeFreshnessScript(script); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{
		"207e4696d3c05f7cb05966aee557307151f1f00217af4143c1bcaf33b8df733f",
		"d11f6b21c61b4274e182eb888883a8ba8acdbf820dcc7a6d82a7d9fc2fd2836d",
		"https://registry-1.docker.io",
		"https://dl-cdn.alpinelinux.org/alpine",
		"verify_manifest(manifest, headers, expected_digest)",
		"_verify_pkcs1_sha1(index_stream, signatures[names[0]], key)",
	} {
		changed := strings.Replace(string(script), from, "removed", 1)
		if changed == string(script) {
			t.Fatal("fixture not mutated")
		}
		if RuntimeFreshnessScript([]byte(changed)) == nil {
			t.Fatalf("runtime freshness safeguard accepted after removing %q", from)
		}
	}
}

func TestWorkflowPrivilegeAndGateMutations(t *testing.T) {
	for _, tc := range []struct{ name, file, from, to string }{
		{"privileged-pr", "ci.yml", "  contents: read", "  contents: write"},
		{"top-level-packages", "ci.yml", "  contents: read", "  contents: read\n  packages: write"},
		{"top-level-identity", "ci.yml", "  contents: read", "  contents: read\n  id-token: write"},
		{"job-write", "ci.yml", "  quality:\n", "  quality:\n    permissions:\n      packages: write\n"},
		{"missing-gate", "release.yml", "needs: [trust, ci, security, container]", "needs: [trust, ci]"},
		{"unprotected-release", "release.yml", "environment: release", "environment: staging"},
		{"artifact-retention", "ci.yml", "retention-days: 7", "retention-days: 90"},
		{"skipped-aggregate", "ci.yml", "if: always()", "if: success()"},
		{"no-op-aggregate", "ci.yml", "run: jq -e 'length == 10 and all(.[]; .result == \"success\")' <<< \"$RESULTS\"", "run: 'true'"},
		{"missing-visual", "ci.yml", "paperless, visual, cross-build", "paperless, cross-build"},
		{"untested-promotion", "release.yml", "candidate, candidate-runtime]", "candidate]"},
		{"automatic-build-record", "container.yml", "DOCKER_BUILD_RECORD_UPLOAD: 'false'", "DOCKER_BUILD_RECORD_UPLOAD: 'true'"},
		{"unverified-container-buildx", "container.yml", "bash scripts/install-ci-tool.sh buildx", "docker buildx version"},
		{"unverified-release-buildx", "release.yml", "bash scripts/install-ci-tool.sh buildx", "docker buildx version"},
		{"unverified-visual-buildx", "ci.yml", "bash scripts/install-ci-tool.sh buildx", "docker buildx version"},
		{"missing-runtime-freshness", "security.yml", "python3 scripts/check-runtime-fresh.py", "echo runtime-check-removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range []string{"ci.yml", "security.yml", "container.yml", "release.yml"} {
				b, err := os.ReadFile("../../.github/workflows/" + file) // #nosec G304 -- Read only the four literal workflow filenames in the repository to construct adversarial policy fixtures.
				if err != nil {
					t.Fatal(err)
				}
				if file == tc.file {
					b = []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
					changed := strings.Replace(string(b), tc.from, tc.to, 1)
					if changed == string(b) {
						t.Fatal("fixture not mutated")
					}
					b = []byte(changed)
				}
				if err := os.WriteFile(dir+"/"+file, b, 0600); err != nil { // #nosec G703 -- file is one of four literal workflow names and dir is this test's TempDir.
					t.Fatal(err)
				}
			}
			if Workflows(dir) == nil {
				t.Fatal("unsafe workflow accepted")
			}
		})
	}
}

func TestScrub(t *testing.T) {
	input := "{\"Action\":\"output\",\"Output\":\"secret /home/alice/token\"}\n{\"Action\":\"pass\",\"Package\":\"github.com/kiefer-networks/invoice-generator/internal/auth\",\"Test\":\"TestSecurity\",\"Elapsed\":1}\n"
	got, err := TestSummary([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "secret") || strings.Contains(string(got), "/home/") || !strings.Contains(string(got), "TestSecurity") {
		t.Fatal(string(got))
	}
	if _, err = TestSummary([]byte(`{"Action":"skip","Test":"TestBrowserWorkflow"}`)); err == nil {
		t.Fatal("skipped browser accepted")
	}
	if _, err = Coverage([]byte("mode: atomic\n/home/alice/a.go:1.1,2.2 1 0\n")); err == nil {
		t.Fatal("absolute path accepted")
	}
	if _, err = TestSummary([]byte("{\"Action\":\"pass\",\"Package\":\"github.com/kiefer-networks/invoice-generator/internal/auth\"}\n")); err == nil {
		t.Fatal("no matching test accepted")
	}
}
