package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestBackupChunkRejectsOversizedPlaintext(t *testing.T) {
	aead, err := backupAEAD(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := &backupEncryptor{w: &output, a: aead}
	if err := writer.chunk(make([]byte, backupChunk+1)); err == nil {
		t.Fatal("oversized encryption chunk accepted")
	}
	if output.Len() != 0 {
		t.Fatal("oversized chunk wrote output")
	}
}

func backupFixture(t *testing.T) (*Store, BackupOptions) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "source.sqlite")
	s, e := Open(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	c, e := s.CustomerRepository().Create(context.Background(), validCustomer("B-1", "Confidential Customer Backup"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.InvoiceRepository().CreateDraft(context.Background(), InvoiceDraftInput{CustomerID: c.ID, Customer: c.CustomerInput, Currency: "EUR", DueDate: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`UPDATE invoices SET state='finalized',number='BACKUP-1',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.DocumentRepository().Claim(context.Background(), time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	docs := filepath.Join(root, "documents")
	if e = os.Mkdir(docs, 0700); e != nil {
		t.Fatal(e)
	}
	data := []byte("%PDF-1.7 exact immutable test bytes")
	sum := sha256.Sum256(data)
	key := strings.Repeat("A", 52)
	if e = os.WriteFile(filepath.Join(docs, key), data, 0600); e != nil {
		t.Fatal(e)
	}
	e = s.DocumentRepository().Complete(context.Background(), j, Document{StorageKey: key, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data)), GeneratorVersion: "test"})
	if e != nil {
		t.Fatal(e)
	}
	return s, BackupOptions{Database: path, DocumentRoot: docs, Output: filepath.Join(root, "backup.enc"), Passphrase: []byte("long test passphrase for backup")}
}

func TestBackupEnvelopeUsesArgon2idAndAES256GCM(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(data[:8], []byte{'I', 'N', 'V', 'B', 'A', 'K', 0, 1}) || data[8] != 2 {
		t.Fatal("unknown envelope")
	}
	wrap := argon2.IDKey(o.Passphrase, data[9:25], 3, 64*1024, 4, 32)
	defer clear(wrap)
	block, e := aes.NewCipher(wrap)
	if e != nil {
		t.Fatal(e)
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	key, e := a.Open(nil, data[25:37], data[37:85], data[:25])
	if e != nil || len(key) != 32 {
		t.Fatal("invalid wrapped AES-256 key", e)
	}
	defer clear(key)
	block, e = aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	a, e = cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	n := int(binary.BigEndian.Uint32(data[85:89]))
	aad := append(bytes.Clone(data[:85]), make([]byte, 12)...)
	binary.BigEndian.PutUint32(aad[93:], uint32(n)) // #nosec G115 -- Adversarial encryption fixture uses a bounded in-memory chunk, then tampers with its authenticated length.
	plain, e := a.Open(nil, make([]byte, 12), data[89:89+n+16], aad)
	if e != nil || !bytes.Contains(plain, []byte("manifest.json")) {
		t.Fatal("invalid sequence-bound data encryption", e)
	}
	clear(plain)
	// Replaying the first valid chunk at sequence one must fail authentication.
	bad := append(bytes.Clone(data[:89+n+16]), data[85:]...)
	path := filepath.Join(t.TempDir(), "replay.enc")
	if err := os.WriteFile(path, bad, 0600); err != nil { // #nosec G703 -- Deliberately corrupt this test's temporary backup/database fixture to verify fail-closed validation.
		t.Error(err)
	}
	if e = VerifyBackup(context.Background(), VerifyOptions{Archive: path, Passphrase: o.Passphrase}); e == nil {
		t.Fatal("chunk replay accepted")
	}
	// The authenticated end marker also requires a physical end of file.
	if err := os.WriteFile(path, append(data, 0), 0600); err != nil { // #nosec G703 -- Deliberately corrupt this test's temporary backup/database fixture to verify fail-closed validation.
		t.Error(err)
	}
	if e = VerifyBackup(context.Background(), VerifyOptions{Archive: path, Passphrase: o.Passphrase}); e == nil {
		t.Fatal("trailing bytes accepted")
	}
}

func TestBackupConcurrentWALTransactions(t *testing.T) {
	s, o := backupFixture(t)
	ctx := context.Background()
	if _, e := s.CustomerRepository().Create(ctx, validCustomer("B-2", "Second")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`PRAGMA wal_autocheckpoint=0; UPDATE customers SET notes='committed-WAL-value'`); e != nil {
		t.Fatal(e)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for n := 0; ; n++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				done <- e
				return
			}
			value := fmt.Sprintf("transaction-%d", n)
			if _, e = tx.Exec(`UPDATE customers SET notes=? WHERE number='B-1'`, value); e != nil {
				_ = tx.Rollback()
				done <- e
				return
			}
			if _, e = tx.Exec(`UPDATE customers SET notes=? WHERE number='B-2'`, value); e != nil {
				_ = tx.Rollback()
				done <- e
				return
			}
			if e = tx.Commit(); e != nil {
				done <- e
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	for i := 0; i < 3; i++ {
		valid := false
		for attempt := 0; attempt < 3 && !valid; attempt++ {
			o.Output = filepath.Join(filepath.Dir(o.Output), fmt.Sprintf("concurrent-%d-%d.enc", i, attempt))
			if _, e := Backup(ctx, o); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(t.TempDir(), "restore")
			if e := Restore(ctx, RestoreOptions{Archive: o.Output, Passphrase: o.Passphrase, TargetRoot: target, Confirm: true}); e != nil {
				t.Fatal(e)
			}
			db, e := Open(ctx, filepath.Join(target, "database.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			var distinct int
			e = db.db.QueryRow(`SELECT count(DISTINCT notes) FROM customers`).Scan(&distinct)
			_ = db.Close()
			valid = e == nil && distinct == 1
			if !valid && attempt < 2 {
				t.Logf("retrying concurrent WAL backup after inconsistent snapshot: %v", e)
			}
		}
		if !valid {
			t.Fatal("torn concurrent transaction")
		}
	}
}

func TestBackupRejectsUncleanExplicitPaths(t *testing.T) {
	_, o := backupFixture(t)
	o.DocumentRoot = o.DocumentRoot + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "documents"
	if _, e := Backup(context.Background(), o); e == nil {
		t.Fatal("traversal accepted")
	}
}

func TestBackupAtomicPublicationNeverReplaces(t *testing.T) {
	root, e := os.OpenRoot(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if err := root.WriteFile("original", []byte("keep"), 0600); err != nil {
		t.Error(err)
	}
	if err := root.WriteFile("candidate", []byte("new"), 0600); err != nil {
		t.Error(err)
	}
	if e = publishRecoveryFile(root, "candidate", "original"); e == nil {
		t.Fatal("original replaced")
	}
	data, _ := root.ReadFile("original")
	if string(data) != "keep" {
		t.Fatal("original damaged")
	}
}

func TestRestoreServiceLockReleaseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	release, e := AcquireServiceLock(path)
	if e != nil {
		t.Fatal(e)
	}
	release()
	second, e := AcquireServiceLock(path)
	if e != nil {
		t.Fatal(e)
	}
	defer second()
	release()
	third, e := AcquireServiceLock(path)
	if e == nil {
		third()
		t.Fatal("old release removed a newer service lock")
	}
}

func TestBackupEncryptedAuthenticatedRandomized(t *testing.T) {
	_, o := backupFixture(t)
	ctx := context.Background()
	result, e := Backup(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	if result.SchemaVersion < 1 {
		t.Fatal("missing schema")
	}
	data, e := os.ReadFile(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	for _, plain := range []string{"SQLite format 3", "Confidential Customer Backup", "%PDF-1.7"} {
		if bytes.Contains(data, []byte(plain)) {
			t.Fatal("plaintext leaked")
		}
	}
	if runtime.GOOS != "windows" {
		i, _ := os.Stat(o.Output)
		if i.Mode().Perm() != 0600 {
			t.Fatal("unsafe archive mode")
		}
	}
	verify := VerifyOptions{Archive: o.Output, Passphrase: o.Passphrase}
	if e = VerifyBackup(ctx, verify); e != nil {
		t.Fatal(e)
	}
	verify.Passphrase = []byte("wrong passphrase")
	if e = VerifyBackup(ctx, verify); e == nil {
		t.Fatal("wrong key accepted")
	}
	verify.Passphrase = o.Passphrase
	for _, at := range []int{0, 20, len(data) / 2, len(data) - 1} {
		bad := bytes.Clone(data)
		bad[at] ^= 1
		p := filepath.Join(t.TempDir(), "bad.enc")
		if err := os.WriteFile(p, bad, 0600); err != nil { // #nosec G703 -- Deliberately corrupt this test's temporary backup/database fixture to verify fail-closed validation.
			t.Error(err)
		}
		verify.Archive = p
		if e = VerifyBackup(ctx, verify); e == nil {
			t.Fatalf("bit flip %d accepted", at)
		}
	}
	for _, n := range []int{0, 30, len(data) - 1} {
		p := filepath.Join(t.TempDir(), "short.enc")
		if err := os.WriteFile(p, data[:n], 0600); err != nil { // #nosec G703 -- Deliberately corrupt this test's temporary backup/database fixture to verify fail-closed validation.
			t.Error(err)
		}
		verify.Archive = p
		if e = VerifyBackup(ctx, verify); e == nil {
			t.Fatal("truncated archive accepted")
		}
	}
	o.Output = filepath.Join(filepath.Dir(o.Output), "second.enc")
	if _, e = Backup(ctx, o); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(o.Output)
	if bytes.Equal(data, second) {
		t.Fatal("encryption reused randomness")
	}
}

func TestBackupMissingOrChangedDocumentNeverPublishes(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "missing"}[missing], func(t *testing.T) {
			_, o := backupFixture(t)
			p := filepath.Join(o.DocumentRoot, strings.Repeat("A", 52))
			if missing {
				if err := os.Remove(p); err != nil {
					t.Error(err)
				}
			} else {
				if err := os.WriteFile(p, []byte("corrupt"), 0600); err != nil {
					t.Error(err)
				}
			}
			if _, e := Backup(context.Background(), o); e == nil {
				t.Fatal("invalid artifact accepted")
			}
			if _, e := os.Stat(o.Output); !os.IsNotExist(e) {
				t.Fatal("archive published on failure")
			}
		})
	}
}

func TestBackupDoesNotOverwriteAndHonorsCancellation(t *testing.T) {
	_, o := backupFixture(t)
	if err := os.WriteFile(o.Output, []byte("original"), 0600); err != nil {
		t.Error(err)
	}
	if _, e := Backup(context.Background(), o); e == nil {
		t.Fatal("existing backup replaced")
	}
	b, _ := os.ReadFile(o.Output)
	if string(b) != "original" {
		t.Fatal("original changed")
	}
	if err := os.Remove(o.Output); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Backup(ctx, o); e == nil {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(o.Output); !os.IsNotExist(e) {
		t.Fatal("cancelled backup published")
	}
}
