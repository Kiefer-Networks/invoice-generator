package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func seedRowidHistory(t *testing.T, s *Store, table string, victim int64) {
	t.Helper()
	ctx := context.Background()
	invoiceRowid := int64(10)
	lineRowid := int64(11)
	if table == "invoices" {
		invoiceRowid = victim
	} else {
		lineRowid = victim
	}
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO customers(id,number,display_name,country,currency) VALUES('customer','C','Buyer','DE','EUR')`, nil},
		{`INSERT INTO invoices(rowid,id,customer_id,state,currency,customer_snapshot) VALUES(?,'historical','customer','draft','EUR','{"DisplayName":"Original recipient"}')`, []any{invoiceRowid}},
		{`INSERT INTO invoices(rowid,id,customer_id,state,currency) VALUES(20,'attacker','customer','draft','EUR')`, nil},
		{`INSERT INTO invoice_items(rowid,id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES(20,'attacker-line','attacker',1,'Draft line',10000,100,1900)`, nil},
	}
	if table == "invoice_items" {
		statements = append(statements, struct {
			q    string
			args []any
		}{`INSERT INTO invoice_items(rowid,id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES(?,'historical-line','historical',1,'Immutable line',10000,123,1900)`, []any{lineRowid}})
	}
	statements = append(statements, struct {
		q    string
		args []any
	}{`UPDATE invoices SET state='finalized',number='HIST-1',company_snapshot='{"LegalName":"Original issuer"}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{"immutable":"historical snapshot"}',invoice_sequence=1 WHERE id='historical'`, nil})
	statements = append(statements, struct {
		q    string
		args []any
	}{`INSERT INTO invoice_finalization_keys(key,invoice_id,draft_version,company_snapshot,reviewed_at) VALUES('history-key','historical',1,'{}','2026-09-06T00:00:00Z')`, nil})
	for _, stmt := range statements {
		if _, err := s.DB().ExecContext(ctx, stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// Capture every normalized column, snapshot byte string, and physical rowid.
func invoiceRowidState(t *testing.T, conn *sql.Conn) map[string][][]any {
	t.Helper()
	out := map[string][][]any{}
	for _, table := range []string{"invoices", "invoice_items", "invoice_finalization_keys"} {
		rows, err := conn.QueryContext(context.Background(), `SELECT rowid,* FROM `+table+` ORDER BY rowid`) // #nosec G202 -- Table name comes from the three literal invoice table names in the loop; no external value enters SQL.
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			out[table] = append(out[table], values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return out
}
func rowidAttack(table, operation, alias string) string {
	if operation == "update" {
		id := "attacker"
		if table == "invoice_items" {
			id = "attacker-line"
		}
		return fmt.Sprintf(`UPDATE OR REPLACE %s SET %s=? WHERE id='%s'`, table, alias, id)
	}
	if table == "invoices" {
		return fmt.Sprintf(`INSERT OR REPLACE INTO invoices(%s,id,customer_id,state,currency) VALUES(?,'incoming','customer','draft','EUR')`, alias)
	}
	return fmt.Sprintf(`INSERT OR REPLACE INTO invoice_items(%s,id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES(?,'incoming-line','attacker',2,'Replacement line',10000,999,0)`, alias)
}
func TestFinalizationRowidReplacementGuards(t *testing.T) {
	for _, table := range []string{"invoices", "invoice_items"} {
		for _, operation := range []string{"update", "insert"} {
			for _, alias := range []string{"rowid", "_rowid_", "oid"} {
				for _, victim := range []int64{1, -1} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d", table, operation, alias, victim), func(t *testing.T) {
						s := openMigratedStore(t)
						seedRowidHistory(t, s, table, victim)
						ctx := context.Background()
						conn, err := s.DB().Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						if _, err = conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`); err != nil {
							t.Fatal(err)
						}
						before := invoiceRowidState(t, conn)
						_, err = conn.ExecContext(ctx, rowidAttack(table, operation, alias), victim)
						if err == nil {
							t.Errorf("accepted %s replacement through %s", operation, alias)
						}
						after := invoiceRowidState(t, conn)
						if !reflect.DeepEqual(before, after) {
							t.Fatal("historical/draft rows or frozen snapshot changed")
						}
					})
				}
			}
		}
	}
}
func TestFinalizationRowidGuardsPreserveAutomaticAndExplicitDraftOperations(t *testing.T) {
	for _, table := range []string{"invoices", "invoice_items"} {
		t.Run(table, func(t *testing.T) {
			s := openMigratedStore(t)
			seedRowidHistory(t, s, table, -1)
			ctx := context.Background()
			conn, err := s.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`); err != nil {
				t.Error(err)
			}
			for _, q := range []string{
				`INSERT INTO invoices(id,customer_id,state,currency) VALUES('new-draft','customer','draft','EUR')`,
				`UPDATE invoices SET _rowid_=1000 WHERE id='new-draft'`,
				`INSERT INTO invoice_items(id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES('new-line','new-draft',1,'New',10000,100,0)`,
				`UPDATE invoice_items SET oid=1000,title_snapshot='Edited' WHERE id='new-line'`,
				`DELETE FROM invoice_items WHERE id='new-line'`,
				`INSERT OR REPLACE INTO invoices(rowid,id,customer_id,state,currency) VALUES(1000,'replacement-draft','customer','draft','EUR')`,
				`DELETE FROM invoices WHERE id='replacement-draft'`,
			} {
				if _, err = conn.ExecContext(ctx, q); err != nil {
					t.Fatalf("legitimate draft operation rejected: %s: %v", q, err)
				}
			}
		})
	}
}
func TestFinalizationRowidMigrationUpgrades008WithoutChangingHistory(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "v8.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:8] {
		if err = s.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	seedRowidHistory(t, s, "invoice_items", -1)
	conn, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := invoiceRowidState(t, conn)
	_ = conn.Close()
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range migrations[:8] {
		var checksum string
		if err = s.DB().QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&checksum); err != nil || checksum != m.checksum {
			t.Fatalf("migration %d checksum changed", m.version)
		}
	}
	conn, err = s.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`); err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(before, invoiceRowidState(t, conn)) {
		t.Fatal("upgrade changed historical bytes or rowids")
	}
	if _, err = conn.ExecContext(ctx, rowidAttack("invoice_items", "insert", "oid"), -1); err == nil {
		t.Fatal("upgraded historical position displaced")
	}
	if !reflect.DeepEqual(before, invoiceRowidState(t, conn)) {
		t.Fatal("replacement changed upgraded records")
	}
}
func TestFinalizationRowidMultirowReplaceRollsBackAfterVacuum(t *testing.T) {
	for _, table := range []string{"invoices", "invoice_items"} {
		t.Run(table, func(t *testing.T) {
			s := openMigratedStore(t)
			seedRowidHistory(t, s, table, -1)
			ctx := context.Background()
			if _, err := s.DB().Exec(`VACUUM`); err != nil {
				t.Fatal(err)
			}
			conn, err := s.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(ctx, `PRAGMA recursive_triggers=OFF`); err != nil {
				t.Fatal(err)
			}
			before := invoiceRowidState(t, conn)
			victimID := "historical"
			if table == "invoice_items" {
				victimID = "historical-line"
			}
			var victimRowid int64
			if err = conn.QueryRowContext(ctx, `SELECT rowid FROM `+table+` WHERE id=?`, victimID).Scan(&victimRowid); err != nil {
				t.Fatal(err)
			}
			if table == "invoices" {
				_, err = conn.ExecContext(ctx, `INSERT OR REPLACE INTO invoices(rowid,id,customer_id,state,currency) VALUES(1000,'first-insert','customer','draft','EUR'),(?,'displacer','customer','draft','EUR')`, victimRowid)
			} else {
				_, err = conn.ExecContext(ctx, `INSERT OR REPLACE INTO invoice_items(oid,id,invoice_id,position,title_snapshot,quantity_scaled,net_unit_price_minor,tax_rate_scaled) VALUES(1000,'first-line','attacker',2,'First',10000,100,0),(?,'displacer-line','attacker',3,'Second',10000,999,0)`, victimRowid)
			}
			if err == nil {
				t.Fatal("multirow replacement accepted")
			}
			if !reflect.DeepEqual(before, invoiceRowidState(t, conn)) {
				t.Fatal("replacement failed to roll back the complete multirow statement")
			}
			var pending int
			if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM invoice_rowid_replacement_checks`).Scan(&pending); err != nil || pending != 0 {
				t.Fatalf("replacement check leaked=%d %v", pending, err)
			}
		})
	}
}
