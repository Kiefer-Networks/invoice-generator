package store

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type RestoreOptions struct {
	Archive, KeyFile, TargetRoot string
	Passphrase                   []byte
	// Confirm explicitly acknowledges activating this backup in a NEW root.
	// Existing roots are never replaced, even when Confirm is true.
	Confirm bool
}

func Restore(ctx context.Context, o RestoreOptions) (err error) {
	defer func() {
		if err != nil {
			err = ErrBackup
		}
	}()
	if !o.Confirm || ctx.Err() != nil || !filepath.IsAbs(o.TargetRoot) || filepath.Dir(o.TargetRoot) == o.TargetRoot {
		return ErrBackup
	}
	parent, e := safeRoot(filepath.Dir(o.TargetRoot))
	if e != nil {
		return e
	}
	defer parent.Close()
	name := filepath.Base(o.TargetRoot)
	if _, e = parent.Lstat(name); !os.IsNotExist(e) {
		return ErrBackup
	}
	lock, e := parent.OpenFile(name+".restore-lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	lock.Close()
	defer parent.Remove(name + ".restore-lock")
	stage, e := privateTemp(filepath.Dir(o.TargetRoot), ".restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	if e = unpackRecovery(ctx, VerifyOptions{Archive: o.Archive, KeyFile: o.KeyFile, Passphrase: o.Passphrase}, stage); e != nil {
		return e
	}
	if e = recordRecoveryAudit(ctx, filepath.Join(stage, "database.sqlite"), "backup.restored"); e != nil {
		return e
	}
	if _, e = inspectRecovery(ctx, filepath.Join(stage, "database.sqlite"), filepath.Join(stage, "documents"), stage); e != nil {
		return e
	}
	if e = syncRecoveryDirectory(filepath.Join(stage, "documents")); e != nil {
		return e
	}
	if e = syncRecoveryDirectory(stage); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e = activateRecoveryRoot(parent, filepath.Base(stage), name); e != nil {
		return e
	}
	return syncRecoveryDirectory(filepath.Dir(o.TargetRoot))
}

// AcquireServiceLock excludes another normal writer for the same database.
// An unclean exit deliberately leaves the lock: an operator must stop all
// processes and remove that exact lock before reopening. No PID guessing.
func AcquireServiceLock(database string) (func(), error) {
	path, e := filepath.Abs(database)
	if e != nil {
		return nil, ErrBackup
	}
	root, e := safeRoot(filepath.Dir(path))
	if e != nil {
		return nil, ErrBackup
	}
	name := filepath.Base(path) + ".service-lock"
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		root.Close()
		return nil, ErrBackup
	}
	f.Close()
	return func() { root.Remove(name); root.Close() }, nil
}

// IntegrityCheck verifies a consistent private snapshot without modifying the
// live database, including migration identity and all referenced artifacts.
func IntegrityCheck(ctx context.Context, database, documentRoot string) (err error) {
	defer func() {
		if err != nil {
			err = ErrBackup
		}
	}()
	source, e := safeRegular(database, backupMaxDatabase)
	if e != nil {
		return e
	}
	source.Close()
	stage, e := privateTemp(filepath.Dir(database), ".integrity-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	db, e := openRecoveryDB(ctx, database)
	if e != nil {
		return e
	}
	defer db.Close()
	path := filepath.Join(stage, "database.sqlite")
	if _, e = db.ExecContext(ctx, `VACUUM INTO ?`, path); e != nil {
		return e
	}
	_, e = inspectRecovery(ctx, path, documentRoot, stage)
	return e
}

// VerifyBackup authenticates every byte and verifies the archive inventory,
// SQLite integrity, foreign keys, exact migrations/schema and document hashes.
// Private plaintext staging is removed on every return, including cancellation.
func VerifyBackup(ctx context.Context, o VerifyOptions) (err error) {
	defer func() {
		if err != nil {
			err = ErrBackup
		}
	}()
	parent, e := safeRoot(filepath.Dir(o.Archive))
	if e != nil {
		return e
	}
	parent.Close()
	stage, e := privateTemp(filepath.Dir(o.Archive), ".verify-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	return unpackRecovery(ctx, o, stage)
}

func unpackRecovery(ctx context.Context, o VerifyOptions, stage string) error {
	input, e := safeRegular(o.Archive, backupMaxBytes+(backupMaxBytes/backupChunk+1)*20+backupHeaderSize)
	if e != nil {
		return e
	}
	defer input.Close()
	plainPath := filepath.Join(stage, "payload.tar")
	plain, e := os.OpenFile(plainPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer func() { plain.Close(); os.Remove(plainPath) }()
	if e = decryptBackup(ctx, input, plain, o.Passphrase, o.KeyFile); e != nil {
		return e
	}
	if _, e = plain.Seek(0, 0); e != nil {
		return e
	}
	if e = preflightRecoveryTar(ctx, plain); e != nil {
		return e
	}
	if _, e = plain.Seek(0, 0); e != nil {
		return e
	}
	tr := tar.NewReader(&recoveryReader{ctx: ctx, r: plain})
	h, e := tr.Next()
	if e != nil || h.Name != "manifest.json" || h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > backupMaxManifest || h.Format != tar.FormatUSTAR {
		return ErrBackup
	}
	encoded, e := io.ReadAll(io.LimitReader(tr, backupMaxManifest+1))
	if e != nil {
		return e
	}
	var m backupManifest
	dec := json.NewDecoder(strings.NewReader(string(encoded)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&m); e != nil {
		return ErrBackup
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return ErrBackup
	}
	ms, e := embeddedMigrations()
	if e != nil {
		return e
	}
	if m.Format != backupFormat || m.Application != backupApplication || m.SchemaVersion != ms[len(ms)-1].version || !m.SessionsExcluded || len(m.Files) < 1 || len(m.Files) > backupMaxFiles {
		return ErrBackup
	}
	if _, e = time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil {
		return ErrBackup
	}
	docs := filepath.Join(stage, "documents")
	if e = os.Mkdir(docs, 0700); e != nil {
		return e
	}
	seen := map[string]bool{}
	var total int64
	for index, entry := range m.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		limit := backupMaxDocument
		if index == 0 {
			if entry.Name != "database.sqlite" {
				return ErrBackup
			}
			limit = backupMaxDatabase
		} else if !strings.HasPrefix(entry.Name, "documents/") || !backupKeyPattern.MatchString(strings.TrimPrefix(entry.Name, "documents/")) {
			return ErrBackup
		}
		if seen[entry.Name] || entry.Size <= 0 || entry.Size > limit || len(entry.SHA256) != 64 {
			return ErrBackup
		}
		seen[entry.Name] = true
		total += entry.Size
		if total > backupMaxBytes {
			return ErrBackup
		}
		h, e = tr.Next()
		if e != nil || h.Typeflag != tar.TypeReg || h.Name != entry.Name || h.Size != entry.Size || h.Linkname != "" || h.Format != tar.FormatUSTAR {
			return ErrBackup
		}
		f, e := os.OpenFile(filepath.Join(stage, filepath.FromSlash(entry.Name)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(f, hash), tr)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil || n != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return ErrBackup
		}
	}
	if _, e = tr.Next(); e != io.EOF {
		return ErrBackup
	}
	var extra [1]byte
	if n, e := plain.Read(extra[:]); n != 0 || e != io.EOF {
		return ErrBackup
	}
	actual, e := inspectRecovery(ctx, filepath.Join(stage, "database.sqlite"), docs, stage)
	if e != nil {
		return e
	}
	if m.SchemaVersion != actual.SchemaVersion || !reflect.DeepEqual(m.Files, actual.Files) {
		return ErrBackup
	}
	db, e := openRecoveryDB(ctx, filepath.Join(stage, "database.sqlite"))
	if e != nil {
		return e
	}
	defer db.Close()
	var sessions int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); e != nil || sessions != 0 {
		return ErrBackup
	}
	return nil
}

// Reject extensions before archive/tar can parse their metadata or sparse maps.
// The only accepted encoding is bounded, ordinary USTAR with no compression.
func preflightRecoveryTar(ctx context.Context, f *os.File) error {
	info, e := f.Stat()
	if e != nil {
		return e
	}
	var header, zero [512]byte
	for count := 0; count <= backupMaxFiles+1; count++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		if _, e = io.ReadFull(f, header[:]); e != nil {
			return ErrBackup
		}
		if bytes.Equal(header[:], zero[:]) {
			if _, e = io.ReadFull(f, header[:]); e != nil || !bytes.Equal(header[:], zero[:]) {
				return ErrBackup
			}
			var b [1]byte
			if n, e := f.Read(b[:]); n != 0 || e != io.EOF {
				return ErrBackup
			}
			return nil
		}
		if header[156] != tar.TypeReg || string(header[257:265]) != "ustar\x0000" {
			return ErrBackup
		}
		sizeText := strings.Trim(string(header[124:136]), " \x00")
		if sizeText == "" {
			return ErrBackup
		}
		for _, c := range sizeText {
			if c < '0' || c > '7' {
				return ErrBackup
			}
		}
		size, e := strconv.ParseInt(sizeText, 8, 64)
		limit := backupMaxDocument
		if count == 0 {
			limit = backupMaxManifest
		} else if count == 1 {
			limit = backupMaxDatabase
		}
		if e != nil || size <= 0 || size > limit {
			return ErrBackup
		}
		pos, e := f.Seek((size+511)/512*512, io.SeekCurrent)
		if e != nil || pos > info.Size() {
			return ErrBackup
		}
	}
	return ErrBackup
}

func inspectRecovery(ctx context.Context, path, docs, work string) (backupManifest, error) {
	var m backupManifest
	db, e := openRecoveryDB(ctx, path)
	if e != nil {
		return m, e
	}
	defer db.Close()
	if e = checkRecoveryDatabase(ctx, db, work); e != nil {
		return m, e
	}
	ms, e := embeddedMigrations()
	if e != nil {
		return m, e
	}
	m.SchemaVersion = ms[len(ms)-1].version
	entry, e := hashRecoveryFile(ctx, path, "database.sqlite", backupMaxDatabase)
	if e != nil {
		return m, e
	}
	m.Files = append(m.Files, entry)
	root, e := safeRoot(docs)
	if e != nil {
		return m, e
	}
	defer root.Close()
	rows, e := db.QueryContext(ctx, `SELECT storage_key,size_bytes,checksum_sha256 FROM documents WHERE status='ready' ORDER BY storage_key`)
	if e != nil {
		return m, e
	}
	defer rows.Close()
	var total int64 = entry.Size
	for rows.Next() {
		var key, sum string
		var size int64
		if e = rows.Scan(&key, &size, &sum); e != nil {
			return m, e
		}
		if !backupKeyPattern.MatchString(key) || size <= 0 || size > backupMaxDocument || len(m.Files) >= backupMaxFiles {
			return m, ErrBackup
		}
		entry, e = hashRecoveryFile(ctx, filepath.Join(docs, key), "documents/"+key, backupMaxDocument)
		if e != nil || entry.Size != size || entry.SHA256 != sum {
			return m, ErrBackup
		}
		total += size
		if total > backupMaxBytes {
			return m, ErrBackup
		}
		m.Files = append(m.Files, entry)
	}
	if e = rows.Err(); e != nil {
		return m, e
	}
	return m, nil
}
func hashRecoveryFile(ctx context.Context, path, name string, max int64) (backupEntry, error) {
	f, e := safeRegular(path, max)
	if e != nil {
		return backupEntry{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(&recoveryReader{ctx: ctx, r: f}, max+1))
	if e != nil || n <= 0 || n > max {
		return backupEntry{}, ErrBackup
	}
	return backupEntry{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func checkRecoveryDatabase(ctx context.Context, db *sql.DB, work string) error {
	var integrity string
	if e := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); e != nil || integrity != "ok" {
		return ErrBackup
	}
	rows, e := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if e != nil || bad {
		return ErrBackup
	}
	ms, e := embeddedMigrations()
	if e != nil {
		return e
	}
	rows, e = db.QueryContext(ctx, `SELECT version,name,checksum FROM schema_migrations ORDER BY version`)
	if e != nil {
		return e
	}
	n := 0
	for rows.Next() {
		var version int
		var name, sum string
		if e = rows.Scan(&version, &name, &sum); e != nil || n >= len(ms) || version != ms[n].version || name != ms[n].name || sum != ms[n].checksum {
			rows.Close()
			return ErrBackup
		}
		n++
	}
	e = rows.Err()
	rows.Close()
	if e != nil || n != len(ms) {
		return ErrBackup
	}
	// Compare actual tables/indexes/triggers, not just the migration ledger. This
	// detects removed immutability guards or a forged ledger in a damaged backup.
	referencePath := filepath.Join(work, "reference.sqlite")
	defer func() { os.Remove(referencePath); os.Remove(referencePath + "-wal"); os.Remove(referencePath + "-shm") }()
	reference, e := Open(ctx, referencePath)
	if e != nil {
		return e
	}
	defer reference.Close()
	if e = reference.Migrate(ctx); e != nil {
		return e
	}
	want, e := recoverySchema(ctx, reference.db)
	if e != nil {
		return e
	}
	got, e := recoverySchema(ctx, db)
	if e != nil || !reflect.DeepEqual(want, got) {
		return ErrBackup
	}
	// Legacy final invoices may lack the newer frozen snapshot. Preserve them,
	// but reject malformed snapshot JSON; schema identity ensures immutable guards.
	var invalid int
	e = db.QueryRowContext(ctx, `SELECT count(*) FROM invoices WHERE state<>'draft' AND (NOT json_valid(company_snapshot) OR NOT json_valid(customer_snapshot) OR NOT json_valid(payment_snapshot) OR NOT json_valid(locale_snapshot) OR NOT json_valid(tax_snapshot) OR NOT json_valid(note_snapshot) OR (frozen_snapshot IS NOT NULL AND NOT json_valid(frozen_snapshot)))`).Scan(&invalid)
	if e != nil || invalid != 0 {
		return ErrBackup
	}
	return nil
}
func recoverySchema(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, e := db.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var kind, name, table, ddl string
		if e = rows.Scan(&kind, &name, &table, &ddl); e != nil {
			return nil, e
		}
		result = append(result, kind+"\x00"+name+"\x00"+table+"\x00"+ddl)
	}
	return result, rows.Err()
}
