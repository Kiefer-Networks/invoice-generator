// Package cipolicy enforces repository delivery policy without downloading tools.
package cipolicy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var actionSHA = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)

func ActionPins(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	return checkUses(&doc)
}

func checkUses(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value != "uses" {
				continue
			}
			value := node.Content[i+1]
			ref := value.Value
			if value.Kind != yaml.ScalarNode {
				return fmt.Errorf("action reference must be a literal")
			}
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "$/") {
				continue
			}
			if !actionSHA.MatchString(ref) || value.LineComment == "" {
				return fmt.Errorf("action requires full commit SHA and version provenance: %s", ref)
			}
		}
	}
	for _, child := range node.Content {
		if err := checkUses(child); err != nil {
			return err
		}
	}
	return nil
}

func Container(compose, dockerfile []byte) error {
	var c struct {
		Services map[string]struct {
			User        string   `yaml:"user"`
			ReadOnly    bool     `yaml:"read_only"`
			CapDrop     []string `yaml:"cap_drop"`
			CapAdd      []string `yaml:"cap_add"`
			Privileged  bool     `yaml:"privileged"`
			NetworkMode string   `yaml:"network_mode"`
			Security    []string `yaml:"security_opt"`
			Ports       []string `yaml:"ports"`
			Tmpfs       []string `yaml:"tmpfs"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &c); err != nil {
		return err
	}
	s, ok := c.Services["invoice"]
	if !ok {
		return fmt.Errorf("invoice service missing")
	}
	if !regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`).MatchString(s.User) || !s.ReadOnly || s.Privileged || s.NetworkMode != "" || len(s.CapAdd) > 0 || strings.Join(s.CapDrop, ",") != "ALL" {
		return fmt.Errorf("container user/rootfs/capability/network policy failed")
	}
	if !strings.Contains(strings.Join(s.Security, ","), "no-new-privileges:true") || strings.Contains(strings.Join(s.Security, ","), "unconfined") {
		return fmt.Errorf("container security options unsafe")
	}
	if len(s.Ports) == 0 {
		return fmt.Errorf("explicit loopback binding required")
	}
	for _, p := range s.Ports {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			return fmt.Errorf("public container binding")
		}
	}
	if len(s.Tmpfs) == 0 {
		return fmt.Errorf("bounded temporary storage required")
	}
	for _, p := range s.Tmpfs {
		for _, required := range []string{"noexec", "nosuid", "nodev"} {
			if !strings.Contains(p, required) {
				return fmt.Errorf("unsafe tmpfs")
			}
		}
		if !regexp.MustCompile(`size=[1-9][0-9]*`).MatchString(p) {
			return fmt.Errorf("unbounded tmpfs")
		}
	}
	return productionStage(dockerfile)
}

// Follow local stage inheritance. External base settings are deliberately not
// trusted: production must inherit an explicit nonroot USER and healthcheck.
func productionStage(data []byte) error {
	type config struct{ user, health string }
	stages := map[string]config{}
	stage := ""
	var logical string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		logical += line
		if strings.HasSuffix(logical, "\\") {
			logical = strings.TrimSuffix(logical, "\\") + " "
			continue
		}
		fields := strings.Fields(logical)
		logical = ""
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			if len(fields) < 4 || !strings.EqualFold(fields[len(fields)-2], "AS") {
				return fmt.Errorf("named container stages required")
			}
			base := fields[1]
			if strings.HasPrefix(base, "--platform=") {
				base = fields[2]
			}
			stage = strings.ToLower(fields[len(fields)-1])
			if _, exists := stages[stage]; exists {
				return fmt.Errorf("duplicate container stage")
			}
			stages[stage] = stages[strings.ToLower(base)]
		case "USER", "HEALTHCHECK":
			c := stages[stage]
			value := strings.Join(fields[1:], " ")
			if strings.EqualFold(fields[0], "USER") {
				c.user = value
			} else {
				c.health = value
			}
			stages[stage] = c
		}
	}
	c, found := stages["production"]
	if !found || !regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`).MatchString(c.user) {
		return fmt.Errorf("numeric nonroot production image user required")
	}
	if !strings.HasPrefix(c.health, "--") || !strings.Contains(c.health, " CMD ") {
		return fmt.Errorf("production image healthcheck required")
	}
	return nil
}

func Workflows(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return err
	}
	yamlPaths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return err
	}
	paths = append(paths, yamlPaths...)
	if len(paths) < 4 {
		return fmt.Errorf("all four delivery workflows required")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{"ci.yml", "security.yml", "container.yml", "release.yml"} {
		if _, err := root.Stat(name); err != nil {
			return fmt.Errorf("required workflow %s is missing", name)
		}
	}
	for _, p := range paths {
		b, e := root.ReadFile(filepath.Base(p))
		if e != nil {
			return e
		}
		if e = ActionPins(b); e != nil {
			return fmt.Errorf("%s: %w", p, e)
		}
		var w struct {
			Permissions map[string]string `yaml:"permissions"`
			Concurrency any               `yaml:"concurrency"`
			On          map[string]any    `yaml:"on"`
			Env         map[string]string `yaml:"env"`
			Jobs        map[string]struct {
				Uses        string            `yaml:"uses"`
				Timeout     int               `yaml:"timeout-minutes"`
				Steps       []map[string]any  `yaml:"steps"`
				Permissions map[string]string `yaml:"permissions"`
				Needs       []string          `yaml:"needs"`
				Environment string            `yaml:"environment"`
				If          string            `yaml:"if"`
			} `yaml:"jobs"`
		}
		if e = yaml.Unmarshal(b, &w); e != nil {
			return e
		}
		if w.Permissions["contents"] != "read" || w.Concurrency == nil {
			return fmt.Errorf("%s: read-only default and concurrency required", p)
		}
		for key, permission := range w.Permissions {
			if key != "contents" || permission != "read" {
				return fmt.Errorf("only contents: read is allowed at workflow level")
			}
		}
		file := filepath.Base(p)
		if (file == "container.yml" || file == "release.yml") && w.Env["DOCKER_BUILD_RECORD_UPLOAD"] != "false" {
			return fmt.Errorf("automatic unsanitized build records must be disabled")
		}
		requiredJobs := map[string][]string{
			"ci.yml":        {"quality", "tests", "migrations", "oidc", "browser", "documents", "numbering", "paperless", "visual", "cross-build"},
			"security.yml":  {"workflow-policy", "secrets", "dependencies", "dependency-review", "codeql-analysis", "freshness"},
			"container.yml": {"images", "local-parity"},
		}
		if required, ok := requiredJobs[file]; ok {
			gate, exists := w.Jobs["required"]
			if !exists || gate.If != "always()" || len(gate.Needs) != len(required) {
				return fmt.Errorf("complete always-running required gate missing")
			}
			for _, name := range required {
				if _, ok := w.Jobs[name]; !ok || !contains(gate.Needs, name) {
					return fmt.Errorf("required job %s absent from gate", name)
				}
			}
			if len(gate.Steps) != 1 || gate.Steps[0]["run"] != fmt.Sprintf("jq -e 'length == %d and all(.[]; .result == \"success\")' <<< \"$RESULTS\"", len(required)) {
				return fmt.Errorf("required gate must reject failed, skipped and cancelled dependencies")
			}
		}
		if file == "security.yml" {
			freshness, ok := w.Jobs["freshness"]
			if !ok {
				return fmt.Errorf("security.yml/freshness: job is missing")
			}
			if err := RuntimeFreshnessSteps(freshness.Steps); err != nil {
				return fmt.Errorf("security.yml/freshness: %w", err)
			}
		}
		if file == "release.yml" {
			for _, name := range []string{"trust", "ci", "security", "container", "candidate", "candidate-runtime", "promote"} {
				if _, ok := w.Jobs[name]; !ok {
					return fmt.Errorf("release job missing: %s", name)
				}
			}
			if !contains(w.Jobs["promote"].Needs, "candidate") || !contains(w.Jobs["promote"].Needs, "candidate-runtime") {
				return fmt.Errorf("promotion must wait for scanned candidate and runtime tests")
			}
		}
		if _, ok := w.On["pull_request_target"]; ok {
			return fmt.Errorf("privileged PR trigger forbidden")
		}
		if strings.Contains(string(b), "@latest") || strings.Contains(string(b), "version: latest") {
			return fmt.Errorf("mutable installer forbidden")
		}
		for name, j := range w.Jobs {
			if err := BuildxSteps(j.Steps); err != nil {
				return fmt.Errorf("%s/%s: %w", file, name, err)
			}
			for _, permission := range j.Permissions {
				if permission == "write" && (file != "release.yml" || (name != "candidate" && name != "promote")) {
					return fmt.Errorf("write permission outside release is forbidden")
				}
			}
			if filepath.Base(p) == "release.yml" && (name == "candidate" || name == "promote") {
				if j.Environment != "release" {
					return fmt.Errorf("protected release environment required")
				}
				for _, required := range []string{"trust", "ci", "security", "container"} {
					found := false
					for _, need := range j.Needs {
						found = found || need == required
					}
					if !found {
						return fmt.Errorf("release missing required gate %s", required)
					}
				}
			}
			if j.Uses == "" && (j.Timeout < 1 || j.Timeout > 90) {
				return fmt.Errorf("%s/%s: bounded timeout required", p, name)
			}
			for _, s := range j.Steps {
				u, _ := s["uses"].(string)
				if strings.HasPrefix(u, "actions/upload-artifact@") {
					with, _ := s["with"].(map[string]any)
					days, _ := with["retention-days"].(int)
					if days < 1 || days > 7 || with["path"] != "artifacts/" {
						return fmt.Errorf("only scrubbed artifacts with seven-day retention may upload")
					}
				}
				if strings.HasPrefix(u, "actions/checkout@") {
					with, _ := s["with"].(map[string]any)
					if with["persist-credentials"] != false {
						return fmt.Errorf("checkout credentials must not persist")
					}
				}
			}
		}
	}
	return nil
}

// RuntimeFreshnessSteps ensures mutable upstream state is checked as a required,
// fail-closed operation rather than merely reported by the scheduled workflow.
func RuntimeFreshnessSteps(steps []map[string]any) error {
	for _, step := range steps {
		run, _ := step["run"].(string)
		if strings.Contains(run, "python3 scripts/check-runtime-fresh.py") {
			if _, conditional := step["if"]; conditional || step["continue-on-error"] != nil {
				return fmt.Errorf("runtime freshness verification must run unconditionally")
			}
			return nil
		}
	}
	return fmt.Errorf("runtime freshness verification is missing")
}

// RuntimeFreshnessScript protects the roots of trust and verification calls in
// the stdlib-only runtime checker from accidental weakening.
func RuntimeFreshnessScript(data []byte) error {
	script := string(data)
	for _, required := range []string{
		"https://registry-1.docker.io",
		"https://auth.docker.io/token",
		"https://dl-cdn.alpinelinux.org/alpine",
		"https://alpinelinux.org/keys",
		"207e4696d3c05f7cb05966aee557307151f1f00217af4143c1bcaf33b8df733f",
		"d11f6b21c61b4274e182eb888883a8ba8acdbf820dcc7a6d82a7d9fc2fd2836d",
		"ARCHITECTURES = (\"x86_64\", \"aarch64\")",
		"REPOSITORIES = (\"main\", \"community\")",
		"verify_manifest(manifest, headers, expected_digest)",
		"_verify_pkcs1_sha1(index_stream, signatures[names[0]], key)",
		"direct_pins = select_direct_pins(pin.packages_by_arch)",
		"check_package_pins(direct_pins, fetch_package_indexes(pin.branch))",
	} {
		if !strings.Contains(script, required) {
			return fmt.Errorf("runtime freshness safeguard missing: %s", required)
		}
	}
	return nil
}

// BuildxSteps forbids installer actions that download unchecked executables and
// requires the checksum-verified installer before direct or scripted Docker builds.
func BuildxSteps(steps []map[string]any) error {
	verified := false
	for _, step := range steps {
		uses, _ := step["uses"].(string)
		if strings.HasPrefix(uses, "docker/setup-buildx-action@") {
			return fmt.Errorf("buildx action downloads are not checksum-verified")
		}
		run, _ := step["run"].(string)
		if strings.TrimSpace(run) == "bash scripts/install-ci-tool.sh buildx" {
			if _, conditional := step["if"]; conditional || step["continue-on-error"] != nil {
				return fmt.Errorf("buildx verification must run unconditionally and fail closed")
			}
			verified = true
		}
		for _, command := range []string{"docker buildx", "scripts/ci-local.sh", "scripts/ci-local.ps1", "scripts/test-visual.sh"} {
			if strings.Contains(run, command) && !verified {
				return fmt.Errorf("buildx checksum verification must precede use")
			}
		}
	}
	return nil
}

func BuildxInstaller(data []byte) error {
	script := string(data)
	for _, required := range []string{
		"buildx) version=0.37.0; repo=docker/buildx; asset=buildx-v${version}.linux-amd64; sha=ae43fa08c796b44efc86d7a63c55f73f7c35f3101188dea7bf93bcd6f99577ba",
		"set -euo pipefail",
		"sha256sum --check --status",
		`install -m 0755 "$archive" "$plugin_dir/docker-buildx"`,
	} {
		if !strings.Contains(script, required) {
			return fmt.Errorf("verified Buildx release pin or installation guard missing")
		}
	}
	if strings.Index(script, "sha256sum --check --status") > strings.Index(script, "install -m 0755") {
		return fmt.Errorf("buildx checksum must be checked before installation")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// TestSummary discards ALL arbitrary test output; only bounded, validated result fields survive.
func TestSummary(data []byte) ([]byte, error) {
	if len(data) > 100<<20 {
		return nil, fmt.Errorf("test evidence exceeds size limit")
	}
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	count := 0
	tests := 0
	for scanner.Scan() {
		var v struct {
			Action, Package, Test string
			Elapsed               float64
		}
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			return nil, err
		}
		if v.Action == "skip" {
			allowed := map[string]bool{"TestContainerRuntime": true, "TestContainerRecoveryDrill": true, "TestContainerMountPreflight": true, "TestComposeRuntimeState": true, "TestComposeBrowserWorkflow": true, "TestComposeBrowserPersistence": true, "TestPrepareRecoveryFixture": true, "TestTestScriptsStopOnGitFailure/test.ps1": true}
			if !allowed[v.Test] {
				return nil, fmt.Errorf("unexpected skipped test: %s", v.Test)
			}
		}
		if v.Action != "pass" && v.Action != "fail" && v.Action != "skip" {
			continue
		}
		// Subtest names can contain user input, URLs and local paths. Keep only
		// top-level Go identifiers; top-level results already include subtest failure.
		if strings.Contains(v.Test, "/") {
			continue
		}
		if v.Test != "" && v.Action != "skip" {
			tests++
		}
		if !regexp.MustCompile(`^github.com/kiefer-networks/invoice-generator(/[A-Za-z0-9_/-]+)?$`).MatchString(v.Package) || !regexp.MustCompile(`^[A-Za-z0-9_]*$`).MatchString(v.Test) {
			return nil, fmt.Errorf("unsafe result identifier")
		}
		if err := json.NewEncoder(&out).Encode(v); err != nil {
			return nil, err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count == 0 || tests == 0 {
		return nil, fmt.Errorf("no test results")
	}
	return out.Bytes(), nil
}

func Coverage(data []byte) ([]byte, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 || lines[0] != "mode: atomic" {
		return nil, fmt.Errorf("atomic coverage required")
	}
	pattern := regexp.MustCompile(`^github.com/kiefer-networks/invoice-generator/[A-Za-z0-9_/-]+\.go:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ [0-9]+ [0-9]+$`)
	for _, line := range lines[1:] {
		if !pattern.MatchString(line) {
			return nil, fmt.Errorf("unsafe coverage path")
		}
	}
	return data, nil
}
