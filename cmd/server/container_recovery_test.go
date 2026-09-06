package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestContainerRecoveryDrill(t *testing.T) {
	if os.Getenv("INVOICE_CONTAINER_TEST") != "1" {
		t.Skip("required CI-local recovery gate")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("invoice-ci-recovery-data-%d", time.Now().UnixNano())
	backups := data + "-backups"
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("docker", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, out)
		}
	}
	defer func() { run("volume", "rm", data, backups) }()
	run("volume", "create", data)
	run("volume", "create", backups)
	run("run", "--rm", "--network=none", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--mount=type=volume,source="+data+",target=/data", "--mount=type=volume,source="+backups+",target=/backup", "--mount=type=volume,source=invoice-ci_development,target=/development,readonly", "--entrypoint=/bin/sh", "invoice-generator:test", "-c", `umask 077; cp /development/state/development.sqlite /data/database/invoice.sqlite && cp -a /development/state/documents/. /data/documents/ && head -c 32 /dev/urandom > /data/backup-key && sync -f /data && stat -c "%u %a %n" /data /data/database /data/documents /backup /data/backup-key`)
	run("run", "--rm", "--network=none", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--mount=type=volume,source="+data+",target=/data", "-e", "INVOICE_RECOVERY_FIXTURE=1", "--entrypoint=/usr/local/bin/browser.test", "invoice-generator:development", "-test.run=^TestPrepareRecoveryFixture$", "-test.v")
	bash := "bash"
	if runtime.GOOS == "windows" {
		bash = "C:/Program Files/Git/bin/bash.exe"
	}
	cmd := exec.Command(bash, "scripts/recovery-drill.sh", "verified")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "COMPOSE_FILE=compose.yaml:docker/compose.recovery-test.yaml", "COMPOSE_PATH_SEPARATOR=:", "COMPOSE_PROJECT_NAME=invoice-recovery-ci", "INVOICE_DRILL_DATA_VOLUME="+data, "INVOICE_DRILL_BACKUP_VOLUME="+backups, "INVOICE_DRILL_KEY_FILE=/data/backup-key", "MSYS_NO_PATHCONV=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recovery drill: %v\n%s", err, out)
	}
	t.Log(string(out))
}
