package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryServiceRejectsDatabaseAliases(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			s, o := backupFixture(t)
			release, e := AcquireServiceLock(o.Database)
			if e != nil {
				t.Fatal(e)
			}
			defer release()
			alias := filepath.Join(filepath.Dir(o.Database), "alias.sqlite")
			if kind == "hardlink" {
				e = os.Link(o.Database, alias)
			} else {
				alias = createRecoverySymlinkAlias(t, o.Database, alias)
			}
			if e != nil {
				t.Skip("filesystem does not support link", e)
			}
			other, e := AcquireServiceLock(alias)
			if e == nil {
				other()
				t.Fatal("alias acquired independent writer lock")
			}
			var n int
			if e = s.db.QueryRow(`SELECT count(*) FROM customers`).Scan(&n); e != nil || n != 1 {
				t.Fatal("live database damaged", e)
			}
			for _, suffix := range []string{"-wal", "-shm", ".service-lock"} {
				if filepath.Base(alias) == filepath.Base(o.Database) {
					continue
				}
				if _, e = os.Lstat(alias + suffix); !os.IsNotExist(e) {
					t.Fatal("alias sidecar created", suffix)
				}
			}
		})
	}
}

func TestRecoveryServiceRejectsLinkedJournal(t *testing.T) {
	s, o := backupFixture(t)
	s.Close()
	secret := filepath.Join(filepath.Dir(o.Database), "unrelated")
	if e := os.WriteFile(secret, []byte("preserve unrelated bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(secret, o.Database+"-journal"); e != nil {
		t.Fatal(e)
	}
	release, e := AcquireServiceLock(o.Database)
	if e == nil {
		release()
		t.Fatal("linked rollback journal accepted")
	}
	data, e := os.ReadFile(secret)
	if e != nil || string(data) != "preserve unrelated bytes" {
		t.Fatal("unrelated file changed", e)
	}
}

func TestRecoveryRejectsUntrustedParents(t *testing.T) {
	for _, kind := range []string{"database", "output", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, o := backupFixture(t)
			if _, e := Backup(context.Background(), o); e != nil {
				t.Fatal(e)
			}
			unsafe := t.TempDir()
			makeRecoveryParentUntrusted(t, unsafe)
			if kind == "database" {
				s.Close()
				data, e := os.ReadFile(o.Database)
				if e != nil {
					t.Fatal(e)
				}
				o.Database = filepath.Join(unsafe, "source.sqlite")
				os.WriteFile(o.Database, data, 0600)
				o.Output = filepath.Join(t.TempDir(), "new.enc")
				if _, e = Backup(context.Background(), o); e == nil {
					t.Fatal("untrusted source parent accepted")
				}
			} else if kind == "output" {
				o.Output = filepath.Join(unsafe, "new.enc")
				if _, e := Backup(context.Background(), o); e == nil {
					t.Fatal("untrusted output parent accepted")
				}
			} else {
				target := filepath.Join(unsafe, "restored")
				if e := Restore(context.Background(), RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e == nil {
					t.Fatal("untrusted restore parent accepted")
				}
			}
		})
	}
}

func TestRecoveryFinalizationFailuresPreserveOriginals(t *testing.T) {
	for _, kind := range []string{"publish", "sync", "audit"} {
		t.Run(kind, func(t *testing.T) {
			_, o := backupFixture(t)
			fail := errors.New("injected boundary failure")
			hooks := recoveryHooks{}
			if kind == "publish" {
				hooks.publish = func(*os.Root, string, string) error { return fail }
			}
			if kind == "sync" {
				hooks.syncDirectory = func(string) error { return fail }
			}
			if kind == "audit" {
				hooks.audit = func(context.Context, string, string) error { return fail }
			}
			if _, e := backupWithHooks(context.Background(), o, hooks); e == nil {
				t.Fatal("failed boundary reported success")
			}
			if kind == "publish" {
				if _, e := os.Stat(o.Output); !os.IsNotExist(e) {
					t.Fatal("failed publication exposed archive")
				}
			} else if e := VerifyBackup(context.Background(), VerifyOptions{Archive: o.Output, Passphrase: o.Passphrase}); e != nil {
				t.Fatal("completed archive damaged after uncertain publication", e)
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(o.Output), ".backup-*"))
			if len(stages) != 0 {
				t.Fatal("plaintext staging retained")
			}
		})
	}
}

func TestRecoveryRestoreFailuresAndTargetCreation(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"sync", "audit", "target-created"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target")
			hooks := recoveryHooks{}
			if kind == "sync" {
				hooks.syncDirectory = func(string) error { return errors.New("sync failure") }
			}
			if kind == "audit" {
				hooks.audit = func(context.Context, string, string) error { return errors.New("audit failure") }
			}
			if kind == "target-created" {
				hooks.beforeActivation = func(string) {
					os.Mkdir(target, 0700)
					os.WriteFile(filepath.Join(target, "original"), []byte("preserve"), 0600)
				}
			}
			if e := restoreWithHooks(context.Background(), RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}, hooks); e == nil {
				t.Fatal("unsafe activation succeeded")
			}
			if kind == "target-created" {
				data, e := os.ReadFile(filepath.Join(target, "original"))
				if e != nil || string(data) != "preserve" {
					t.Fatal("newly created target overwritten")
				}
			} else if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("failed restore activated")
			}
			stages, _ := filepath.Glob(filepath.Join(parent, ".restore-*"))
			if len(stages) != 0 {
				t.Fatal("failed restore retained plaintext")
			}
		})
	}
}

func TestRecoveryRejectsReplaceableAncestor(t *testing.T) {
	_, o := backupFixture(t)
	ancestor := t.TempDir()
	makeRecoveryParentUntrusted(t, ancestor)
	child := filepath.Join(ancestor, "protected")
	if e := os.Mkdir(child, 0700); e != nil {
		t.Fatal(e)
	}
	if e := protectRecoveryPath(child, true); e != nil {
		t.Fatal(e)
	}
	o.Output = filepath.Join(child, "backup.enc")
	if _, e := Backup(context.Background(), o); e == nil {
		t.Fatal("protected leaf beneath replaceable ancestor accepted")
	}
}

func TestRecoverySnapshotSourceReplacementFailsClosed(t *testing.T) {
	s, o := backupFixture(t)
	s.Close()
	original, e := os.ReadFile(o.Database)
	if e != nil {
		t.Fatal(e)
	}
	moved := o.Database + ".original"
	_, e = backupWithHooks(context.Background(), o, recoveryHooks{afterSourceValidated: func() {
		if e := os.Rename(o.Database, moved); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(o.Database, original, 0600); e != nil {
			t.Fatal(e)
		}
	}})
	if e == nil {
		t.Fatal("replaced source accepted")
	}
	if _, e = os.Stat(o.Output); !os.IsNotExist(e) {
		t.Fatal("replaced source published")
	}
	data, e := os.ReadFile(moved)
	if e != nil || !bytes.Equal(data, original) {
		t.Fatal("original altered", e)
	}
}

func TestRecoveryServiceRejectsReplacementDuringStartup(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[exists], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "database.sqlite")
			if exists {
				os.WriteFile(path, []byte("original"), 0600)
			}
			db, release, e := openServiceWithHook(context.Background(), path, func() {
				if exists {
					if e := os.Rename(path, path+".original"); e != nil {
						t.Fatal(e)
					}
				}
				if e := os.WriteFile(path, []byte("substitute"), 0600); e != nil {
					t.Fatal(e)
				}
			})
			if e == nil {
				db.Close()
				release()
				t.Fatal("substitute opened for service")
			}
			data, e := os.ReadFile(path)
			if e != nil || string(data) != "substitute" {
				t.Fatal("substitute mutated by SQLite", e)
			}
			for _, suffix := range []string{"-wal", "-shm", ".service-lock"} {
				if _, e = os.Stat(path + suffix); !os.IsNotExist(e) {
					t.Fatal("startup failure left sidecar", suffix)
				}
			}
		})
	}
}

func TestRecoveryActivationRejectsStageSubstitution(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "restored")
	var displaced string
	e := restoreWithHooks(context.Background(), RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}, recoveryHooks{beforeActivation: func(stage string) {
		displaced = stage + "-moved"
		if e := os.Rename(stage, displaced); e != nil {
			t.Fatal(e)
		}
		if e := os.Mkdir(stage, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(stage, "sentinel"), []byte("preserve substitute"), 0600); e != nil {
			t.Fatal(e)
		}
	}})
	if e == nil {
		t.Fatal("substituted stage activated")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("unverified data activated")
	}
	entries, e := os.ReadDir(displaced)
	if e != nil || len(entries) != 0 {
		t.Fatal("displaced plaintext not cleaned", e)
	}
}
