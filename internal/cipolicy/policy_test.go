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
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range []string{"ci.yml", "security.yml", "container.yml", "release.yml"} {
				b, err := os.ReadFile("../../.github/workflows/" + file)
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
				if err := os.WriteFile(dir+"/"+file, b, 0600); err != nil {
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
