package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerRuntime(t *testing.T) {
	if os.Getenv("INVOICE_CONTAINER_TEST") != "1" {
		t.Skip("run scripts/ci-local for the required Docker gate")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("docker", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	image := strings.TrimSpace(string(run("image", "inspect", "invoice-generator:test", "--format", "{{.Config.User}}")))
	if image != "65532:65532" {
		t.Fatalf("image user %q", image)
	}
	id := strings.TrimSpace(string(run("run", "-d", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--security-opt=seccomp="+filepath.Join(root, "docker", "seccomp.json"), "--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=536870912", "--shm-size=256m", "--pids-limit=512", "--memory=2g", "--cpus=2", "--ulimit=core=0:0", "--entrypoint=/bin/sh", "invoice-generator:test", "-c", "sleep 180")))
	defer run("rm", "-f", id)
	var config []struct {
		Config     struct{ User string }
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
			SecurityOpt    []string
			PortBindings   map[string]any
		}
	}
	if err := json.Unmarshal(run("inspect", id), &config); err != nil {
		t.Fatal(err)
	}
	if len(config) != 1 || !config[0].HostConfig.ReadonlyRootfs || config[0].Config.User != "65532:65532" || strings.Join(config[0].HostConfig.CapDrop, ",") != "ALL" || len(config[0].HostConfig.PortBindings) != 0 {
		t.Fatalf("unsafe runtime: %+v", config)
	}
	run("exec", id, "sh", "-c", `test "$(id -u)" = 65532 && test ! -e /usr/local/bin/browser.test && test ! -e /src && test ! -e /development-assets && test -z "$(find /run/secrets -type f 2>/dev/null)" && test -s /usr/share/doc/invoice-generator/licenses/go/LICENSE && test -s /usr/share/doc/invoice-generator/licenses/compiled-modules.txt && test ! -w /usr/local/bin && test ! -w /etc && grep -q '^CapEff:.*0000000000000000$' /proc/1/status && grep -q '^NoNewPrivs:.*1$' /proc/1/status`)
	run("exec", id, "sh", "-c", `chromium --headless --disable-gpu --disable-dev-shm-usage --print-to-pdf=/tmp/probe.pdf about:blank >/tmp/chrome.log 2>&1 && test -s /tmp/probe.pdf && java -version && javac -version`)
	cmd := exec.Command("docker", "exec", id, "server", "serve", "-dev")
	if out, err := cmd.CombinedOutput(); err == nil || bytes.Contains(out, []byte("LOCAL DEVELOPMENT")) {
		t.Fatalf("production accepted local fixtures: %v %s", err, out)
	}
	binary := filepath.Join(t.TempDir(), "server")
	run("cp", id+":/usr/local/bin/server", binary)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"LOCAL DEVELOPMENT", "Example Development GmbH", "local-fixture-only", "internal/devmode", "testdata/dev"} {
		if bytes.Contains(data, []byte(marker)) {
			t.Fatalf("image binary contains %s", marker)
		}
	}
}

func TestComposeRuntimeState(t *testing.T) {
	state := os.Getenv("INVOICE_COMPOSE_TEST")
	if state == "" {
		t.Skip("run scripts/ci-local for the required Compose gate")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "compose", "-p", "invoice-ci", "-f", "compose.yaml", "-f", "compose.dev.yaml", "ps", "-a", "-q", "invoice")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatal("Compose service missing")
	}
	out, err = exec.Command("docker", "inspect", id).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var inspect []struct {
		Config     struct{ User string }
		HostConfig struct {
			ReadonlyRootfs bool
			CapDrop        []string
			SecurityOpt    []string
			NetworkMode    string
			PidsLimit      int
			Memory         int64
			NanoCPUs       int64
			PortBindings   map[string]any
		}
		State struct {
			Running  bool
			ExitCode int
			Health   struct{ Status string }
		}
		Mounts []struct {
			Destination string
			RW          bool
		}
	}
	if err := json.Unmarshal(out, &inspect); err != nil || len(inspect) != 1 {
		t.Fatalf("invalid inspect: %v", err)
	}
	c := inspect[0]
	if state == "stopped" {
		if c.State.Running || c.State.ExitCode != 0 {
			t.Fatalf("SIGTERM failed: %+v", c.State)
		}
		return
	}
	if !c.State.Running || c.State.Health.Status != "healthy" || c.Config.User != "65532:65532" || !c.HostConfig.ReadonlyRootfs || strings.Join(c.HostConfig.CapDrop, ",") != "ALL" || c.HostConfig.PidsLimit != 512 || c.HostConfig.Memory != 2<<30 || c.HostConfig.NanoCPUs != 2e9 || len(c.HostConfig.PortBindings) != 0 || c.HostConfig.NetworkMode != "host" || !strings.Contains(strings.Join(c.HostConfig.SecurityOpt, ","), "no-new-privileges") {
		t.Fatalf("unsafe or unhealthy Compose runtime: %+v", c)
	}
	for _, m := range c.Mounts {
		if m.RW && m.Destination != "/development" {
			t.Fatalf("unexpected writable mount: %+v", m)
		}
		if strings.HasPrefix(m.Destination, "/run/secrets") {
			t.Fatal("development has production secret mount")
		}
	}
}
