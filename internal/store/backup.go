package store

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Backups contain no mounted secrets and exclude all local sessions. Version 1
// requires an exact schema match: upgrades and downgrades are separate operations.
const backupFormat = 1
const backupApplication = "invoice-generator/recovery-1"
const backupChunk = 64 << 10
const backupMaxBytes int64 = 1 << 30
const backupMaxDatabase int64 = 256 << 20
const backupMaxDocument int64 = 20 << 20
const backupMaxFiles = 10000
const backupMaxManifest = 4 << 20

var ErrBackup = errors.New("backup validation failed")
var backupKeyPattern = regexp.MustCompile(`^[A-Z2-7]{52}$`)

type BackupOptions struct {
	Database, DocumentRoot, Output string
	// Exactly one is required. The caller retains and must clear Passphrase.
	Passphrase []byte
	KeyFile    string
}
type BackupResult struct {
	SchemaVersion int
	Files         int
	Bytes         int64
}
type VerifyOptions struct {
	Archive, KeyFile string
	Passphrase       []byte
}
type backupEntry struct {
	Name   string
	Size   int64
	SHA256 string
}
type backupManifest struct {
	Format           int
	Application      string
	SchemaVersion    int
	CreatedAt        string
	SessionsExcluded bool
	Files            []backupEntry
}

// Backup uses VACUUM INTO (SQLite's consistent transactional snapshot, including
// committed WAL pages). Ready documents are immutable and published before their
// database references, so concurrent publication cannot create a torn backup.
func Backup(ctx context.Context, o BackupOptions) (result BackupResult, err error) {
	return backupWithHooks(ctx, o, recoveryHooks{})
}

type recoveryHooks struct {
	afterSourceValidated func()
	beforeActivation     func(string)
	publish              func(*os.Root, string, string) error
	syncDirectory        func(string) error
	audit                func(context.Context, string, string) error
}

func (h recoveryHooks) defaults() recoveryHooks {
	if h.publish == nil {
		h.publish = publishRecoveryFile
	}
	if h.syncDirectory == nil {
		h.syncDirectory = syncRecoveryDirectory
	}
	if h.audit == nil {
		h.audit = recordRecoveryAudit
	}
	return h
}

func backupWithHooks(ctx context.Context, o BackupOptions, hooks recoveryHooks) (result BackupResult, err error) {
	hooks = hooks.defaults()
	defer func() {
		if err != nil {
			err = ErrBackup
		}
	}()
	if ctx.Err() != nil || !filepath.IsAbs(o.Database) || !filepath.IsAbs(o.DocumentRoot) || !filepath.IsAbs(o.Output) {
		return result, ErrBackup
	}
	parent, e := safeRoot(filepath.Dir(o.Output))
	if e != nil {
		return result, e
	}
	defer parent.Close()
	output := filepath.Base(o.Output)
	if _, e = parent.Lstat(output); !os.IsNotExist(e) {
		return result, ErrBackup
	}
	// A reservation prevents cooperating backup commands from racing publication.
	reservation, e := parent.OpenFile(output+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	reservation.Close()
	defer parent.Remove(output + ".lock")
	staging, e := newRecoveryStage(parent, filepath.Dir(o.Output), ".backup-")
	if e != nil {
		return result, e
	}
	defer staging.cleanup()
	work := staging.path
	source, e := bindRecoveryDatabase(o.Database, false)
	if e != nil {
		return result, e
	}
	defer source.close()
	if info, e := source.file.Stat(); e != nil || info.Size() > backupMaxDatabase {
		return result, ErrBackup
	}
	if hooks.afterSourceValidated != nil {
		hooks.afterSourceValidated()
	}
	if e = source.check(); e != nil {
		return result, e
	}
	db, e := openRecoveryDB(ctx, o.Database)
	if e != nil {
		return result, e
	}
	defer db.Close()
	if e = source.check(); e != nil {
		return result, e
	}
	snapshot := filepath.Join(work, "database.sqlite")
	if _, e = db.ExecContext(ctx, `VACUUM INTO ?`, snapshot); e != nil {
		return result, e
	}
	if e = source.check(); e != nil {
		return result, e
	}
	if e = staging.check(); e != nil {
		return result, e
	}
	if e = protectRecoveryPath(snapshot, false); e != nil {
		return result, e
	}
	snap, e := Open(ctx, snapshot)
	if e != nil {
		return result, e
	}
	// Erase session hashes in the private snapshot, then vacuum to remove deleted
	// bytes as well. Source authentication and business data remain untouched.
	if _, e = snap.db.ExecContext(ctx, `PRAGMA secure_delete=ON; DELETE FROM sessions; VACUUM; PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
		snap.Close()
		return result, e
	}
	if e = snap.Close(); e != nil {
		return result, e
	}
	manifest, e := inspectRecovery(ctx, snapshot, o.DocumentRoot, work)
	if e != nil {
		return result, e
	}
	manifest.Format = backupFormat
	manifest.Application = backupApplication
	manifest.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manifest.SessionsExcluded = true
	encoded, e := json.Marshal(manifest)
	if e != nil || len(encoded) > backupMaxManifest {
		return result, ErrBackup
	}
	destination, e := os.OpenFile(filepath.Join(work, "archive.enc"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	defer destination.Close()
	enc, e := newBackupEncryptor(destination, o.Passphrase, o.KeyFile)
	if e != nil {
		return result, e
	}
	defer enc.clear()
	tw := tar.NewWriter(enc)
	if e = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded)), Typeflag: tar.TypeReg}); e != nil {
		return result, e
	}
	if _, e = tw.Write(encoded); e != nil {
		return result, e
	}
	for _, entry := range manifest.Files {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		path := snapshot
		if entry.Name != "database.sqlite" {
			path = filepath.Join(o.DocumentRoot, strings.TrimPrefix(entry.Name, "documents/"))
		}
		f, e := safeRegular(path, entry.Size)
		if e != nil {
			return result, e
		}
		if e = tw.WriteHeader(&tar.Header{Name: entry.Name, Mode: 0600, Size: entry.Size, Typeflag: tar.TypeReg}); e != nil {
			f.Close()
			return result, e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(tw, h), io.LimitReader(&recoveryReader{ctx: ctx, r: f}, entry.Size+1))
		f.Close()
		if e != nil || n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return result, ErrBackup
		}
		result.Bytes += n
	}
	if e = tw.Close(); e != nil {
		return result, e
	}
	if e = enc.Close(); e != nil {
		return result, e
	}
	if e = destination.Sync(); e != nil {
		return result, e
	}
	if e = destination.Close(); e != nil {
		return result, e
	}
	// Verify the actual completed ciphertext before it becomes the final archive.
	if e = VerifyBackup(ctx, VerifyOptions{Archive: filepath.Join(work, "archive.enc"), Passphrase: o.Passphrase, KeyFile: o.KeyFile}); e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if _, e = parent.Lstat(output); !os.IsNotExist(e) {
		return result, ErrBackup
	}
	if e = source.check(); e != nil {
		return result, e
	}
	if e = staging.check(); e != nil {
		return result, e
	}
	if e = hooks.publish(parent, filepath.Join(filepath.Base(work), "archive.enc"), output); e != nil {
		return result, e
	}
	if e = hooks.syncDirectory(filepath.Dir(o.Output)); e != nil {
		return result, e
	}
	if e = source.check(); e != nil {
		return result, e
	}
	if e = hooks.audit(ctx, o.Database, "backup.created"); e != nil {
		return result, e
	}
	result.SchemaVersion = manifest.SchemaVersion
	result.Files = len(manifest.Files)
	return result, nil
}

func recordRecoveryAudit(ctx context.Context, database, action string) error {
	binding, e := bindRecoveryDatabase(database, false)
	if e != nil {
		return e
	}
	defer binding.close()
	id, e := newBusinessID()
	if e != nil {
		return e
	}
	db, e := Open(ctx, database)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = binding.check(); e != nil {
		return e
	}
	if _, e = db.db.ExecContext(ctx, `INSERT INTO audit_events(id,action,target_type,result,change_summary) VALUES(?,?,'backup','success','{}')`, id, action); e != nil {
		return e
	}
	if action == "backup.restored" {
		_, e = db.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	if e == nil {
		e = binding.check()
	}
	return e
}

// Link publishes a complete inode without any replacement window. The private
// temporary name is then removed, leaving exactly one link to the final file.
func publishRecoveryFile(root *os.Root, from, to string) error {
	if e := root.Link(from, to); e != nil {
		return e
	}
	if e := root.Remove(from); e != nil {
		return e
	}
	// Flush post-publication file metadata as well (notably on Windows).
	f, e := root.OpenFile(to, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Envelope: fixed header (magic/version, wrapping mode, random salt, wrap nonce,
// wrapped random AES-256 data key), followed by length-delimited GCM chunks.
// Header + sequence + length are authenticated; nonce = zero prefix + sequence.
// An authenticated empty final chunk makes truncation and trailing data invalid.
const backupHeaderSize = 8 + 1 + 16 + 12 + 48

var backupMagic = []byte{'I', 'N', 'V', 'B', 'A', 'K', 0, 1}

type backupEncryptor struct {
	w                io.Writer
	a                cipher.AEAD
	header, key, buf []byte
	seq              uint64
}

func wrappingKey(pass []byte, keyFile string, salt []byte) ([]byte, byte, error) {
	if (len(pass) == 0) == (keyFile == "") {
		return nil, 0, ErrBackup
	}
	if keyFile != "" {
		f, e := safeRegular(keyFile, 32)
		if e != nil {
			return nil, 0, e
		}
		defer f.Close()
		if !protectedRecoveryKey(f) {
			return nil, 0, ErrBackup
		}
		b, e := io.ReadAll(io.LimitReader(f, 33))
		if e != nil || len(b) != 32 {
			clear(b)
			return nil, 0, ErrBackup
		}
		return b, 1, nil
	}
	if len(pass) < 12 || len(pass) > 1024 {
		return nil, 0, ErrBackup
	}
	// Fixed parameters are not attacker controlled: 64 MiB, 3 passes, 4 lanes.
	return argon2.IDKey(pass, salt, 3, 64*1024, 4, 32), 2, nil
}
func backupAEAD(key []byte) (cipher.AEAD, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func newBackupEncryptor(w io.Writer, pass []byte, keyFile string) (*backupEncryptor, error) {
	h := make([]byte, backupHeaderSize)
	copy(h, backupMagic)
	if _, e := rand.Read(h[9:37]); e != nil {
		return nil, e
	}
	wrap, mode, e := wrappingKey(pass, keyFile, h[9:25])
	if e != nil {
		return nil, e
	}
	defer clear(wrap)
	h[8] = mode
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		return nil, e
	}
	wa, e := backupAEAD(wrap)
	if e != nil {
		clear(key)
		return nil, e
	}
	copy(h[37:], wa.Seal(nil, h[25:37], key, h[:25]))
	a, e := backupAEAD(key)
	if e != nil {
		clear(key)
		return nil, e
	}
	if _, e = w.Write(h); e != nil {
		clear(key)
		return nil, e
	}
	return &backupEncryptor{w: w, a: a, header: h, key: key}, nil
}
func (w *backupEncryptor) clear() { clear(w.key); clear(w.buf); w.a = nil }
func chunkBinding(h []byte, seq uint64, n uint32) ([]byte, []byte) {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], seq)
	aad := make([]byte, len(h)+12)
	copy(aad, h)
	binary.BigEndian.PutUint64(aad[len(h):], seq)
	binary.BigEndian.PutUint32(aad[len(h)+8:], n)
	return nonce, aad
}
func (w *backupEncryptor) chunk(p []byte) error {
	n := uint32(len(p))
	nonce, aad := chunkBinding(w.header, w.seq, n)
	sealed := w.a.Seal(nil, nonce, p, aad)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], n)
	if _, e := w.w.Write(size[:]); e != nil {
		return e
	}
	if _, e := w.w.Write(sealed); e != nil {
		return e
	}
	w.seq++
	return nil
}
func (w *backupEncryptor) Write(p []byte) (int, error) {
	count := 0
	for len(p) > 0 {
		n := min(backupChunk-len(w.buf), len(p))
		w.buf = append(w.buf, p[:n]...)
		p = p[n:]
		count += n
		if len(w.buf) == backupChunk {
			if e := w.chunk(w.buf); e != nil {
				return count, e
			}
			clear(w.buf)
			w.buf = w.buf[:0]
		}
	}
	return count, nil
}
func (w *backupEncryptor) Close() error {
	if len(w.buf) > 0 {
		if e := w.chunk(w.buf); e != nil {
			return e
		}
	}
	return w.chunk(nil)
}
func decryptBackup(ctx context.Context, r io.Reader, w io.Writer, pass []byte, keyFile string) error {
	h := make([]byte, backupHeaderSize)
	if _, e := io.ReadFull(r, h); e != nil || !bytes.Equal(h[:8], backupMagic) {
		return ErrBackup
	}
	wrap, mode, e := wrappingKey(pass, keyFile, h[9:25])
	if e != nil {
		return ErrBackup
	}
	defer clear(wrap)
	if mode != h[8] {
		return ErrBackup
	}
	wa, e := backupAEAD(wrap)
	if e != nil {
		return e
	}
	key, e := wa.Open(nil, h[25:37], h[37:], h[:25])
	if e != nil {
		return ErrBackup
	}
	defer clear(key)
	a, e := backupAEAD(key)
	if e != nil {
		return e
	}
	var total int64
	for seq := uint64(0); ; seq++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var b [4]byte
		if _, e = io.ReadFull(r, b[:]); e != nil {
			return ErrBackup
		}
		n := binary.BigEndian.Uint32(b[:])
		if n > backupChunk {
			return ErrBackup
		}
		total += int64(n)
		if total > backupMaxBytes {
			return ErrBackup
		}
		sealed := make([]byte, int(n)+a.Overhead())
		if _, e = io.ReadFull(r, sealed); e != nil {
			return ErrBackup
		}
		nonce, aad := chunkBinding(h, seq, n)
		plain, e := a.Open(nil, nonce, sealed, aad)
		if e != nil {
			return ErrBackup
		}
		if n == 0 {
			var extra [1]byte
			count, e := r.Read(extra[:])
			if count != 0 || e != io.EOF {
				return ErrBackup
			}
			return nil
		}
		_, e = w.Write(plain)
		clear(plain)
		if e != nil {
			return e
		}
	}
}

type recoveryReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *recoveryReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func safeRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrBackup
	}
	volume := filepath.VolumeName(path)
	parts := strings.Split(filepath.ToSlash(path[len(volume):]), "/")
	for _, part := range parts {
		if part == "." || part == ".." || strings.Contains(part, ":") {
			return nil, ErrBackup
		}
	}
	root, e := os.OpenRoot(volume + string(os.PathSeparator))
	if e != nil {
		return nil, e
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		ancestor, e := root.Open(".")
		if e != nil {
			root.Close()
			return nil, e
		}
		trusted := trustedRecoveryAncestor(ancestor)
		ancestor.Close()
		if !trusted {
			root.Close()
			return nil, ErrBackup
		}
		info, e := root.Lstat(part)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, ErrBackup
		}
		next, e := root.OpenRoot(part)
		root.Close()
		if e != nil {
			return nil, e
		}
		opened, e := next.Stat(".")
		if e != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, ErrBackup
		}
		root = next
	}
	directory, e := root.Open(".")
	if e != nil {
		root.Close()
		return nil, e
	}
	trusted := trustedRecoveryDirectory(directory)
	directory.Close()
	if !trusted {
		root.Close()
		return nil, ErrBackup
	}
	return root, nil
}
func safeRegular(path string, max int64) (*os.File, error) {
	root, e := safeRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	name := filepath.Base(path)
	i, e := root.Lstat(name)
	if e != nil || !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 || i.Size() > max {
		return nil, ErrBackup
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	j, e := f.Stat()
	if e != nil || !os.SameFile(i, j) || !j.Mode().IsRegular() || j.Size() > max || !singleRecoveryLink(f) {
		f.Close()
		return nil, ErrBackup
	}
	return f, nil
}
func privateTemp(parent, prefix string) (string, error) {
	p, e := os.MkdirTemp(parent, prefix)
	if e != nil {
		return "", e
	}
	if e = protectRecoveryPath(p, true); e != nil {
		os.Remove(p)
		return "", e
	}
	return p, nil
}
func openRecoveryDB(ctx context.Context, path string) (*sql.DB, error) {
	// A VACUUM INTO image uses DELETE journaling. A read-only verifier must
	// not attempt to convert it to WAL (which would itself require a write).
	dsn := strings.ReplaceAll(sqliteDSN(path), "_pragma=journal_mode%28WAL%29&", "")
	db, e := sql.Open("sqlite", dsn+"&mode=ro")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(2)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
