package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoverySignalChild(t *testing.T) {
	if os.Getenv("INVOICE_RECOVERY_TEST_CHILD") != "1" {
		return
	}
	var args []string
	if e := json.Unmarshal([]byte(os.Getenv("INVOICE_RECOVERY_TEST_ARGS")), &args); e != nil {
		os.Exit(2)
	}
	os.Args = append([]string{"server"}, args...)
	main()
	os.Exit(0)
}

func TestRecoverySignalCleansReservationAndStaging(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "source.sqlite")
	docs := filepath.Join(root, "documents")
	os.Mkdir(docs, 0700)
	key := filepath.Join(root, "key")
	os.WriteFile(key, bytes.Repeat([]byte{4}, 32), 0600)
	archive := filepath.Join(root, "backup.enc")
	db, e := store.Open(context.Background(), database)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	db.DB().SetMaxOpenConns(1)
	if _, e = db.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE); PRAGMA journal_mode=DELETE; BEGIN EXCLUSIVE`); e != nil {
		t.Fatal(e)
	}
	defer db.DB().Exec(`ROLLBACK`)
	args, _ := json.Marshal([]string{"backup", "-database", database, "-document-root", docs, "-output", archive, "-key-file", key})
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoverySignalChild$")
	cmd.Env = append(os.Environ(), "INVOICE_RECOVERY_TEST_CHILD=1", "INVOICE_RECOVERY_TEST_ARGS="+string(args))
	configureRecoverySignalChild(cmd)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	defer func() {
		if !finished {
			cmd.Process.Kill()
			<-done
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		stages, _ := filepath.Glob(filepath.Join(root, ".backup-*"))
		if len(stages) > 0 {
			break
		}
		select {
		case e := <-done:
			finished = true
			t.Fatalf("child exited before staging: %v %s", e, output.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no reservation/staging")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e = interruptRecoveryChild(cmd); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		finished = true
		if e == nil {
			t.Fatal("interrupted backup reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("interrupt did not await and finish cleanup")
	}
	if _, e = os.Stat(archive); !os.IsNotExist(e) {
		t.Fatal("interrupted backup activated")
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".backup-") || strings.HasSuffix(entry.Name(), ".lock") {
			t.Fatalf("interrupt left staging/reservation: %s; child=%s", entry.Name(), output.String())
		}
	}
}
