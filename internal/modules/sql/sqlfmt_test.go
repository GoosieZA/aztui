package sql

import (
	"strings"
	"testing"
)

func TestFormatSQLBasic(t *testing.T) {
	in := "select top 10 a.id, b.name from orders a inner join customers b on a.cid = b.id where a.total > 100 and b.region = 'EU' order by a.total desc"
	out := formatSQL(in)

	for _, want := range []string{
		"SELECT TOP 10 a.id",
		"\nFROM orders a",
		"\nINNER JOIN customers b",
		"\n  ON a.cid = b.id",
		"\nWHERE a.total > 100",
		"\n  AND b.region = 'EU'",
		"\nORDER BY a.total DESC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatSQLPreservesStrings(t *testing.T) {
	in := `SELECT 'from where and' AS s, [order column], "group" FROM t`
	out := formatSQL(in)
	if !strings.Contains(out, "'from where and'") {
		t.Errorf("string literal mangled:\n%s", out)
	}
	if !strings.Contains(out, "[order column]") {
		t.Errorf("bracket identifier mangled:\n%s", out)
	}
	if strings.Contains(out, "\nWHERE AND") {
		t.Errorf("broke inside a string literal:\n%s", out)
	}
}

func TestFormatSQLEscapedQuote(t *testing.T) {
	out := formatSQL(`SELECT 'it''s from x' FROM t`)
	if !strings.Contains(out, "'it''s from x'") {
		t.Errorf("escaped quote mangled:\n%s", out)
	}
	if !strings.Contains(out, "\nFROM t") {
		t.Errorf("FROM clause not broken:\n%s", out)
	}
}

func TestFormatSQLSubqueryCommasStayInline(t *testing.T) {
	out := formatSQL("SELECT a, b FROM t WHERE x IN (SELECT id, 1 FROM u)")
	if !strings.Contains(out, "(SELECT id, 1") {
		t.Errorf("comma inside parens should stay inline:\n%s", out)
	}
	if !strings.Contains(out, "a,\n  b") {
		t.Errorf("top-level select list should break:\n%s", out)
	}
}

func TestFormatSQLQueryStoreParamPrefix(t *testing.T) {
	// Query Store texts often look like "(@P1 int)SELECT ..."
	out := formatSQL("(@P1 int, @P2 nvarchar(40))SELECT * FROM t WHERE a = @P1")
	if !strings.Contains(out, "\nSELECT *") {
		t.Errorf("SELECT after param list should start a new line:\n%s", out)
	}
}

func TestFormatSQLNeverPanicsOnGarbage(t *testing.T) {
	for _, in := range []string{"", "((((", "'unterminated", "[unterminated", "-- only a comment", "/* open"} {
		_ = formatSQL(in) // must not panic
	}
}
