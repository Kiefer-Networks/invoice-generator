package store

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRestoreRoundtripPreservesSnapshotsAndDocuments(t *testing.T) {
	s, o := backupFixture(t)
	ctx := context.Background()
	if _, e := s.db.Exec(`INSERT INTO oidc_users(id,issuer,subject,display_name,email) VALUES('backup-user','issuer','subject','name','mail'); INSERT INTO sessions(id,user_id,token_hash,csrf_secret_hash,expires_at,authorization_expires_at) VALUES('session','backup-user','secret-hash','csrf-hash','2099-01-01','2099-01-01')`); e != nil {
		t.Fatal(e)
	}
	if _, e := Backup(ctx, o); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if e := Restore(ctx, RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e != nil {
		t.Fatal(e)
	}
	db, e := Open(ctx, filepath.Join(target, "database.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var value string
	if e = db.db.QueryRow(`SELECT display_name FROM customers`).Scan(&value); e != nil || value != "Confidential Customer Backup" {
		t.Fatal(value, e)
	}
	var count int
	db.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count)
	if count != 0 {
		t.Fatal("restored live sessions")
	}
	s.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count)
	if count != 1 {
		t.Fatal("source sessions changed")
	}
	if e = db.db.QueryRow(`SELECT frozen_snapshot FROM invoices`).Scan(&value); e != nil || value != "{}" {
		t.Fatal("snapshot changed", e)
	}
	if _, e = db.db.Exec(`UPDATE invoices SET frozen_snapshot='changed'`); e == nil {
		t.Fatal("immutable guard absent")
	}
	data, e := os.ReadFile(filepath.Join(target, "documents", strings.Repeat("A", 52)))
	if e != nil || string(data) != "%PDF-1.7 exact immutable test bytes" {
		t.Fatal("artifact changed", e)
	}
	if e = IntegrityCheck(ctx, filepath.Join(target, "database.sqlite"), filepath.Join(target, "documents")); e != nil {
		t.Fatal(e)
	}
}

func TestBackupRestorePersistOnlySafeAuditOutcomes(t *testing.T) {
	s, o := backupFixture(t)
	ctx := context.Background()
	if _, e := Backup(ctx, o); e != nil {
		t.Fatal(e)
	}
	var summary, result string
	if e := s.db.QueryRow(`SELECT result,change_summary FROM audit_events WHERE action='backup.created'`).Scan(&result, &summary); e != nil || result != "success" || summary != "{}" {
		t.Fatal("missing safe backup audit", e)
	}
	target := filepath.Join(t.TempDir(), "target")
	if e := Restore(ctx, RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e != nil {
		t.Fatal(e)
	}
	db, e := Open(ctx, filepath.Join(target, "database.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.db.QueryRow(`SELECT result,change_summary FROM audit_events WHERE action='backup.restored'`).Scan(&result, &summary); e != nil || result != "success" || summary != "{}" {
		t.Fatal("missing safe restore audit", e)
	}
}

func TestRestorePreflightsTarBeforeExtensionParsing(t *testing.T) {
	for _, kind := range []byte{tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNUSparse, tar.TypeSymlink} {
		t.Run(fmt.Sprintf("type-%d", kind), func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			tw.WriteHeader(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Size: 1, Mode: 0600})
			tw.Write([]byte("x"))
			tw.Close()
			data := b.Bytes()
			data[156] = kind
			path := filepath.Join(t.TempDir(), "tar")
			os.WriteFile(path, data, 0600)
			f, e := os.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			if e = preflightRecoveryTar(context.Background(), f); e == nil {
				t.Fatal("extension header reached tar parser")
			}
		})
	}
}

func TestRestoreVerifiesSchemaLedgerForeignKeysAndSnapshots(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	attacks := map[string]string{
		"malformed-snapshot":        `UPDATE invoices SET frozen_snapshot='not-json'`,
		"ledger-checksum":           `UPDATE schema_migrations SET checksum='forged' WHERE version=1`,
		"newer-ledger":              `INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(999,'future','x','now')`,
		"missing-migration":         `DELETE FROM schema_migrations WHERE version=1`,
		"removed-immutable-trigger": `DROP TRIGGER finalized_invoice_snapshots_are_immutable`,
		"foreign-key-violation":     `PRAGMA foreign_keys=OFF; INSERT INTO invoices(id,customer_id,state,currency) VALUES('orphan','absent','draft','EUR')`,
	}
	for name, query := range attacks {
		t.Run(name, func(t *testing.T) {
			archive := mutateRecoveryArchive(t, o, func(m *backupManifest, files map[string][]byte) {
				path := filepath.Join(t.TempDir(), "mutated.sqlite")
				os.WriteFile(path, files["database.sqlite"], 0600)
				db, e := Open(context.Background(), path)
				if e != nil {
					t.Fatal(e)
				}
				db.db.SetMaxOpenConns(1)
				var savedTrigger string
				if name == "malformed-snapshot" {
					if e = db.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='invoice_final_content_guard'`).Scan(&savedTrigger); e != nil {
						t.Fatal(e)
					}
					if _, e = db.db.Exec(`DROP TRIGGER invoice_final_content_guard`); e != nil {
						t.Fatal(e)
					}
				}
				if _, e = db.db.Exec(query); e != nil {
					db.Close()
					t.Fatal(e)
				}
				if savedTrigger != "" {
					if _, e = db.db.Exec(savedTrigger); e != nil {
						t.Fatal(e)
					}
				}
				if e = db.Close(); e != nil {
					t.Fatal(e)
				}
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				files["database.sqlite"] = b
				m.Files[0].Size = int64(len(b))
				sum := sha256.Sum256(b)
				m.Files[0].SHA256 = hex.EncodeToString(sum[:])
			}, nil)
			if e := VerifyBackup(context.Background(), VerifyOptions{Archive: archive, Passphrase: o.Passphrase}); e == nil {
				t.Fatal("semantically corrupt database accepted")
			}
		})
	}
}

type cancelAfterPlaintextContext struct {
	context.Context
	parent   string
	cancel   context.CancelFunc
	observed atomic.Bool
}

func (c *cancelAfterPlaintextContext) Err() error {
	paths, _ := filepath.Glob(filepath.Join(c.parent, ".restore-*", "payload.tar"))
	for _, p := range paths {
		if i, e := os.Stat(p); e == nil && i.Size() > 0 {
			c.observed.Store(true)
			c.cancel()
		}
	}
	return c.Context.Err()
}
func TestRestoreInterruptedAfterPlaintextWriteCleansStaging(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterPlaintextContext{Context: base, parent: parent, cancel: cancel}
	target := filepath.Join(parent, "target")
	if e := Restore(ctx, RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e == nil {
		t.Fatal("interrupted restore succeeded")
	}
	if !ctx.observed.Load() {
		t.Fatal("test never interrupted partial plaintext")
	}
	entries, e := os.ReadDir(parent)
	if e != nil || len(entries) != 0 {
		t.Fatal("interrupted restore left data", e)
	}
}

func TestRestoreRefusesLiveExistingUnconfirmedAndInterruptedTargets(t *testing.T) {
	_, o := backupFixture(t)
	ctx := context.Background()
	if _, e := Backup(ctx, o); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"existing", "live", "unconfirmed", "cancelled", "wrong-key"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "restored")
			opts := RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}
			runctx := ctx
			if kind == "existing" || kind == "live" {
				os.Mkdir(target, 0700)
				os.WriteFile(filepath.Join(target, "original"), []byte("preserve"), 0600)
			}
			if kind == "live" {
				release, e := AcquireServiceLock(filepath.Join(target, "database.sqlite"))
				if e != nil {
					t.Fatal(e)
				}
				defer release()
			}
			if kind == "unconfirmed" {
				opts.Confirm = false
			}
			if kind == "wrong-key" {
				opts.Passphrase = []byte("wrong long passphrase")
			}
			if kind == "cancelled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				runctx = c
			}
			if e := Restore(runctx, opts); e == nil {
				t.Fatal("unsafe restore accepted")
			}
			if kind == "existing" || kind == "live" {
				data, e := os.ReadFile(filepath.Join(target, "original"))
				if e != nil || string(data) != "preserve" {
					t.Fatal("source damaged")
				}
			} else if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("failed restore activated")
			}
			files, _ := os.ReadDir(parent)
			for _, f := range files {
				if strings.HasPrefix(f.Name(), ".restore-") {
					t.Fatal("plaintext staging left behind")
				}
			}
		})
	}
}

// Re-encrypt a deliberately modified archive with the real key. Authentication
// alone must never replace structural, schema and artifact validation.
func mutateRecoveryArchive(t *testing.T, o BackupOptions, mutate func(*backupManifest, map[string][]byte), extra *tar.Header) string {
	t.Helper()
	data, e := os.ReadFile(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	var plain bytes.Buffer
	if e = decryptBackup(context.Background(), bytes.NewReader(data), &plain, o.Passphrase, ""); e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(bytes.NewReader(plain.Bytes()))
	tr.Next()
	encoded, _ := io.ReadAll(tr)
	var m backupManifest
	if e = json.Unmarshal(encoded, &m); e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		files[h.Name], e = io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
	}
	mutate(&m, files)
	var output bytes.Buffer
	enc, e := newBackupEncryptor(&output, o.Passphrase, "")
	if e != nil {
		t.Fatal(e)
	}
	defer enc.clear()
	tw := tar.NewWriter(enc)
	encoded, _ = json.Marshal(m)
	tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded)), Typeflag: tar.TypeReg})
	tw.Write(encoded)
	for _, entry := range m.Files {
		data, ok := files[entry.Name]
		if !ok {
			continue
		}
		tw.WriteHeader(&tar.Header{Name: entry.Name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write(data)
	}
	if extra != nil {
		tw.WriteHeader(extra)
	}
	tw.Close()
	enc.Close()
	path := filepath.Join(t.TempDir(), "malicious.enc")
	os.WriteFile(path, output.Bytes(), 0600)
	return path
}
func TestRestoreRejectsMaliciousAuthenticatedArchives(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	cases := map[string]func(*backupManifest, map[string][]byte){
		"newer-schema":      func(m *backupManifest, _ map[string][]byte) { m.SchemaVersion++ },
		"older-schema":      func(m *backupManifest, _ map[string][]byte) { m.SchemaVersion-- },
		"wrong-application": func(m *backupManifest, _ map[string][]byte) { m.Application = "other-app" },
		"traversal": func(m *backupManifest, f map[string][]byte) {
			m.Files[1].Name = "../outside"
			f["../outside"] = []byte("evil")
		},
		"absolute": func(m *backupManifest, f map[string][]byte) {
			m.Files[1].Name = "/absolute"
			f["/absolute"] = []byte("evil")
		},
		"missing":           func(m *backupManifest, f map[string][]byte) { delete(f, m.Files[1].Name) },
		"duplicate":         func(m *backupManifest, _ map[string][]byte) { m.Files = append(m.Files, m.Files[1]) },
		"bad-document-hash": func(m *backupManifest, f map[string][]byte) { f[m.Files[1].Name][0] ^= 1 },
		"bad-db-integrity": func(m *backupManifest, f map[string][]byte) {
			b := f["database.sqlite"]
			b[0] ^= 1
			h := sha256.Sum256(b)
			m.Files[0].SHA256 = hex.EncodeToString(h[:])
		},
		"oversized": func(m *backupManifest, _ map[string][]byte) { m.Files[1].Size = 1 << 50 },
		"too-many-files": func(m *backupManifest, _ map[string][]byte) {
			for len(m.Files) <= 10000 {
				m.Files = append(m.Files, m.Files[1])
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			archive := mutateRecoveryArchive(t, o, change, nil)
			target := filepath.Join(t.TempDir(), "target")
			if e := Restore(context.Background(), RestoreOptions{Archive: archive, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e == nil {
				t.Fatal("malicious archive accepted")
			}
			if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("bad archive activated")
			}
		})
	}
	for _, kind := range []byte{tar.TypeReg, tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo} {
		t.Run(string(kind), func(t *testing.T) {
			archive := mutateRecoveryArchive(t, o, func(*backupManifest, map[string][]byte) {}, &tar.Header{Name: "extra", Linkname: "outside", Typeflag: kind, Mode: 0600})
			if e := VerifyBackup(context.Background(), VerifyOptions{Archive: archive, Passphrase: o.Passphrase}); e == nil {
				t.Fatal("extra or special entry accepted")
			}
		})
	}
}

func TestBackupProtectedKeyAndHardlinks(t *testing.T) {
	_, o := backupFixture(t)
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, bytes.Repeat([]byte{7}, 32), 0600)
	o.KeyFile = key
	o.Passphrase = nil
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	if e := VerifyBackup(context.Background(), VerifyOptions{Archive: o.Output, KeyFile: key}); e != nil {
		t.Fatal(e)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if e := os.Link(o.Output, linked); e != nil {
		t.Skip("hardlinks unavailable", e)
	}
	if e := VerifyBackup(context.Background(), VerifyOptions{Archive: o.Output, KeyFile: key}); e == nil {
		t.Fatal("hardlinked archive accepted")
	}
}
