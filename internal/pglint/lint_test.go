package pglint

import (
	"slices"
	"testing"
	"time"
)

func rulesOf(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestLint_EachRuleAndItsSafeForm(t *testing.T) {
	for _, tc := range []struct {
		name, up, rule, safe string
	}{
		{
			"index", "CREATE INDEX orders_email ON orders (email)", RuleIndexLocks,
			"CREATE INDEX CONCURRENTLY orders_email ON orders (email)",
		},
		{
			"unique index on a qualified table", "create unique index if not exists o_e on app.orders(email)", RuleIndexLocks,
			"CREATE TABLE orders (id int); CREATE INDEX o_e ON orders (id)",
		},
		{
			"not null", "ALTER TABLE orders ADD COLUMN region text NOT NULL", RuleNotNullNoDefault,
			"ALTER TABLE orders ADD COLUMN region text NOT NULL DEFAULT 'us'",
		},
		{
			"volatile default", "ALTER TABLE orders ADD COLUMN token uuid DEFAULT gen_random_uuid()", RuleVolatileDefault,
			"ALTER TABLE orders ADD COLUMN created timestamptz DEFAULT now()",
		},
		{
			"type change", "ALTER TABLE orders ALTER COLUMN total TYPE numeric(12,2)", RuleTypeChangeRewrites,
			"ALTER TABLE orders ADD COLUMN total_v2 numeric(12,2)",
		},
		{
			"set data type", "ALTER TABLE orders ALTER total SET DATA TYPE bigint", RuleTypeChangeRewrites,
			"ALTER TABLE orders ALTER total SET DEFAULT 0",
		},
		{
			"foreign key", "ALTER TABLE orders ADD CONSTRAINT orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)",
			RuleConstraintValidates,
			"ALTER TABLE orders ADD CONSTRAINT orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id) NOT VALID",
		},
		{
			"check", "ALTER TABLE orders ADD CHECK (total >= 0)", RuleConstraintValidates,
			"ALTER TABLE orders ADD CONSTRAINT positive CHECK (total >= 0) NOT VALID; ALTER TABLE orders VALIDATE CONSTRAINT positive",
		},
		{"drop table", "DROP TABLE IF EXISTS app.orders CASCADE", RuleDropTable, "CREATE TABLE orders (id int)"},
		{
			"drop column", "ALTER TABLE orders DROP COLUMN IF EXISTS note", RuleDropColumn,
			"ALTER TABLE orders DROP CONSTRAINT orders_note_check",
		},
		{"truncate", "TRUNCATE TABLE ONLY orders, items RESTART IDENTITY", RuleTruncate, "DELETE FROM orders WHERE id = 1"},
		{
			"unbounded delete", "DELETE FROM orders", RuleUnboundedDelete,
			"DELETE FROM orders WHERE created < now() - interval '1 year'",
		},
		{
			"unbounded update", "UPDATE orders SET status = 'open'", RuleUnboundedUpdate,
			"UPDATE orders SET status = 'open' WHERE status IS NULL",
		},
		{"lock timeout raised", "SET lock_timeout = '1min'", RuleLockTimeoutRaised, "SET lock_timeout = '5s'"},
		{"lock timeout off", "SET LOCAL lock_timeout TO 0", RuleLockTimeoutRaised, "SET LOCAL lock_timeout TO 2000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Lint(tc.up, Target{})
			if !slices.Contains(rulesOf(got), tc.rule) {
				t.Errorf("Lint(%q) = %v, want %s", tc.up, rulesOf(got), tc.rule)
			}
			for _, f := range got {
				if f.Effect == "" || f.Statement != 1 || f.Object == "" {
					t.Errorf("finding %+v does not name its statement, object and effect", f)
				}
				if f.Severity == SeverityWarning && f.Safe == "" {
					t.Errorf("warning %s names no safe form", f.Rule)
				}
			}
			safe, _ := Lint(tc.safe, Target{})
			if slices.Contains(rulesOf(safe), tc.rule) {
				t.Errorf("Lint(%q) = %v, the safe form still reports %s", tc.safe, rulesOf(safe), tc.rule)
			}
		})
	}
}

func TestLint_TablesThisMigrationCreatesAreNotLive(t *testing.T) {
	up := `CREATE TABLE IF NOT EXISTS orders (id bigint PRIMARY KEY, email text);
CREATE INDEX orders_email ON orders (email);
ALTER TABLE orders ADD COLUMN region text NOT NULL, ADD CONSTRAINT fk FOREIGN KEY (id) REFERENCES x (id),
  ALTER COLUMN email TYPE varchar(200), DROP COLUMN region;
CREATE TABLE staging (id int); INSERT INTO staging VALUES (1); DELETE FROM staging; TRUNCATE staging; DROP TABLE staging`
	got, _ := Lint(up, Target{})
	if len(got) != 0 {
		t.Errorf("Lint on a table the migration creates = %v, want nothing", rulesOf(got))
	}
}

func TestLint_SeveralActionsAndStatements(t *testing.T) {
	up := `-- a comment naming DROP TABLE x;
ALTER TABLE orders ADD COLUMN a int NOT NULL, DROP COLUMN b;
SELECT 'DELETE FROM orders';
DO $$ BEGIN DELETE FROM orders; END $$;
UPDATE orders SET a = (SELECT max(a) FROM t WHERE t.id = 1)`
	got, _ := Lint(up, Target{})
	want := []string{RuleNotNullNoDefault, RuleDropColumn, RuleUnboundedUpdate}
	if !slices.Equal(rulesOf(got), want) {
		t.Errorf("Lint = %v, want %v", rulesOf(got), want)
	}
	if got[2].Statement != 4 {
		t.Errorf("the UPDATE is statement %d, want 4", got[2].Statement)
	}
	if d := Destructive(got); len(d) != 2 || d[0].Object != "orders.b" {
		t.Errorf("Destructive = %+v, want the dropped column and the unbounded update", d)
	}
}

func TestLint_Renames(t *testing.T) {
	_, renames := Lint(`ALTER TABLE orders RENAME COLUMN email TO contact_email;
ALTER TABLE app.customers RENAME TO clients;
ALTER TABLE orders RENAME CONSTRAINT a TO b;
ALTER TABLE orders RENAME total TO amount`, Target{})
	want := []Rename{
		{Statement: 1, Table: "orders", Column: "email"},
		{Statement: 2, Table: "customers"},
		{Statement: 4, Table: "orders", Column: "total"},
	}
	if !slices.Equal(renames, want) {
		t.Fatalf("renames = %+v, want %+v", renames, want)
	}
	if renames[0].Name() != "email" || renames[0].Object() != "orders.email" || renames[1].Name() != "customers" || renames[1].Object() != "customers" {
		t.Errorf("Name/Object = %q %q %q %q", renames[0].Name(), renames[0].Object(), renames[1].Name(), renames[1].Object())
	}
}

func TestLint_QuotedIdentifiersAndUnreadable(t *testing.T) {
	got, _ := Lint(`DROP TABLE "Order Items"; CREATE INDEX ON; ALTER TABLE; DELETE FROM; SET lock_timeout = 'soon'; SET search_path = app`, Target{})
	if !slices.Equal(rulesOf(got), []string{RuleDropTable}) || got[0].Object != "Order Items" {
		t.Errorf("Lint = %+v, want one drop of the quoted table and nothing for the statements it cannot read", got)
	}
}

func TestLex_DollarOnlyOpensABodyAtADollar(t *testing.T) {
	got := lex(`x$y$ DROP TABLE t; $tag$ body $tag$ $1`)
	words := make([]string, 0, len(got))
	for _, tk := range got {
		words = append(words, tk.text)
	}
	want := []string{"x$y$", "drop", "table", "t", ";", "$$", "$", "1"}
	if !slices.Equal(words, want) {
		t.Errorf("lex = %q, want %q", words, want)
	}
}

func TestPGDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"2000": 2 * time.Second, "5s": 5 * time.Second, "1min": time.Minute, "0": 0, "1.5 h": 90 * time.Minute, "250ms": 250 * time.Millisecond,
	} {
		if got, ok := pgDuration(in); !ok || got != want {
			t.Errorf("pgDuration(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"soon", "5 fortnights", ""} {
		if _, ok := pgDuration(in); ok {
			t.Errorf("pgDuration(%q) read a value", in)
		}
	}
}
