package schemadiff

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

func init() {
	Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
}

func load(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	tables, _ := ParseDump(CleanPastedDDL(string(data)))
	return tables
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./... -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file; diff:\n%s", name,
			UnifiedDiff(strings.Split(string(want), "\n"), strings.Split(got, "\n"), "want", "got", 2))
	}
}

// The golden files were checked against the original Python implementation (mysql_query_advisor/schema_diff.py).
func TestGolden(t *testing.T) {
	src, tgt := load(t, "source.sql"), load(t, "target.sql")
	for _, tc := range []struct {
		name         string
		a, b         map[string]string
		includeDrops bool
	}{
		{"forward", src, tgt, false},
		{"forward_drops", src, tgt, true},
		{"reverse", tgt, src, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := DiffSchemas(tc.a, tc.b, nil, "", "")
			script := r.MigrationScript(ScriptOptions{IncludeDrops: tc.includeDrops, Database: "shop", SourceLabel: "S", TargetLabel: "T"})
			golden(t, tc.name+".sql", script)
			in := ReportInput{Result: r, Script: script, SourceLabel: "S", TargetLabel: "T", IncludeDrops: tc.includeDrops, DDLDiff: true}
			golden(t, tc.name+".txt", TextReport(in))
			golden(t, tc.name+".md", MarkdownReport(in))
		})
	}
}

func diffOne(t *testing.T, src, tgt string) []Change {
	t.Helper()
	s, g := ParseTable(src, ""), ParseTable(tgt, "")
	if s == nil || g == nil {
		t.Fatalf("could not parse: %v %v", s, g)
	}
	return DiffTables(s, g, nil)
}

func find(t *testing.T, changes []Change, kind string) Change {
	t.Helper()
	for _, c := range changes {
		if c.Kind == kind {
			return c
		}
	}
	t.Fatalf("no %s change in %+v", kind, changes)
	return Change{}
}

func TestIdenticalWrittenDifferently(t *testing.T) {
	a := "CREATE TABLE t (id INTEGER(11) NOT NULL, n VARCHAR(10) DEFAULT 5, d DECIMAL(8), ts datetime DEFAULT now(), PRIMARY KEY (id)) ENGINE=InnoDB"
	b := "CREATE TABLE `t` (\n `id` int NOT NULL,\n `n` varchar(10) DEFAULT '5',\n `d` decimal(8,0),\n `ts` datetime DEFAULT CURRENT_TIMESTAMP,\n PRIMARY KEY (`id`)\n) ENGINE=InnoDB"
	if ch := diffOne(t, a, b); len(ch) != 0 {
		t.Errorf("expected no changes, got %+v", ch)
	}
}

func TestRenameColumn(t *testing.T) {
	ch := diffOne(t, "CREATE TABLE t (id int, full_name varchar(20))", "CREATE TABLE t (id int, name varchar(20))")
	c := find(t, ch, "rename_column")
	if c.Clause != "CHANGE COLUMN `name` `full_name` varchar(20)" || c.DataLoss {
		t.Errorf("unexpected rename: %+v", c)
	}
}

func TestVarcharWidening(t *testing.T) {
	c := find(t, diffOne(t, "CREATE TABLE t (a varchar(50)) CHARSET=utf8mb4", "CREATE TABLE t (a varchar(20)) CHARSET=utf8mb4"), "modify_column")
	if c.Severity != SeverityOK || c.Algorithm != AlgoInplace || c.Label != "instant" {
		t.Errorf("same length-byte widening should be instant: %+v", c)
	}
	c = find(t, diffOne(t, "CREATE TABLE t (a varchar(100)) CHARSET=utf8mb4", "CREATE TABLE t (a varchar(20)) CHARSET=utf8mb4"), "modify_column")
	if c.Severity != SeverityWarning || c.Algorithm != AlgoCopy {
		t.Errorf("crossing 255 bytes should copy: %+v", c)
	}
	c = find(t, diffOne(t, "CREATE TABLE t (a varchar(10))", "CREATE TABLE t (a varchar(20))"), "modify_column")
	if c.Severity != SeverityCritical {
		t.Errorf("shrinking should be critical: %+v", c)
	}
	// A sign change is a type change: negative values no longer fit.
	for _, tgt := range []string{"CREATE TABLE t (a int NOT NULL)", "CREATE TABLE t (a int(11) NOT NULL)"} {
		c = find(t, diffOne(t, "CREATE TABLE t (a int unsigned NOT NULL)", tgt), "modify_column")
		if c.Severity != SeverityCritical || !strings.Contains(c.Text, "`int` → `int unsigned`") {
			t.Errorf("signed → unsigned should be critical: %+v", c)
		}
	}
	// Widening plus a character set change copies the table; widening plus NOT NULL rebuilds it.
	c = find(t, diffOne(t, "CREATE TABLE t (a varchar(60) CHARACTER SET utf8mb4)", "CREATE TABLE t (a varchar(50) CHARACTER SET utf8mb3)"), "modify_column")
	if c.Algorithm != AlgoCopy || c.Label != "blocks writes" || strings.Contains(c.Text, "no rebuild") {
		t.Errorf("charset change should copy the table: %+v", c)
	}
	c = find(t, diffOne(t, "CREATE TABLE t (a varchar(60) NOT NULL) CHARSET=utf8mb4", "CREATE TABLE t (a varchar(50)) CHARSET=utf8mb4"), "modify_column")
	if c.Algorithm != AlgoRebuild || c.Label != "online, rebuilds table" || strings.Contains(c.Text, "no rebuild") {
		t.Errorf("NOT NULL should rebuild the table: %+v", c)
	}
}

func TestEnum(t *testing.T) {
	c := find(t, diffOne(t, "CREATE TABLE t (a enum('x','y','z'))", "CREATE TABLE t (a enum('x','y'))"), "modify_column")
	if c.Algorithm != AlgoInstant || c.Severity != SeverityOK {
		t.Errorf("appending enum values is instant: %+v", c)
	}
	c = find(t, diffOne(t, "CREATE TABLE t (a enum('x'))", "CREATE TABLE t (a enum('x','y'))"), "modify_column")
	if c.Severity != SeverityCritical || !strings.Contains(c.Text, "Removes 'y'") {
		t.Errorf("removing enum values is critical: %+v", c)
	}
}

func TestDropsAreCommentedOut(t *testing.T) {
	r := DiffSchemas(map[string]string{"t": "CREATE TABLE t (a int)"},
		map[string]string{"t": "CREATE TABLE t (a int, b int)", "old": "CREATE TABLE old (a int)"}, nil, "", "")
	script := r.MigrationScript(ScriptOptions{})
	if !strings.Contains(script, "-- DROP TABLE `old`;") || !strings.Contains(script, "--   DROP COLUMN `b`;") {
		t.Errorf("drops should be commented out:\n%s", script)
	}
	if got := r.ExecutableStatements(false); len(got) != 0 {
		t.Errorf("nothing should run without drops, got %q", got)
	}
	if got := r.ExecutableStatements(true); len(got) != 2 {
		t.Errorf("expected 2 statements with drops, got %q", got)
	}
}

func TestForeignKeyPhases(t *testing.T) {
	r := DiffSchemas(
		map[string]string{"p": "CREATE TABLE p (id int PRIMARY KEY)",
			"c": "CREATE TABLE c (id int, pid int, CONSTRAINT fk FOREIGN KEY (pid) REFERENCES p (id))"},
		map[string]string{"c": "CREATE TABLE c (id int, pid int)"}, nil, "", "")
	got := r.ExecutableStatements(false)
	if len(got) != 3 || !strings.HasPrefix(got[0], "CREATE TABLE `p`") || !strings.Contains(got[2], "ADD CONSTRAINT `fk` FOREIGN KEY") {
		t.Errorf("foreign keys must be added last: %q", got)
	}
}

func TestForeignKeysMatchByDefinition(t *testing.T) {
	ch := diffOne(t,
		"CREATE TABLE c (pid int, FOREIGN KEY (pid) REFERENCES `db`.`p` (id))",
		"CREATE TABLE c (pid int, KEY pid (pid), CONSTRAINT `c_ibfk_7` FOREIGN KEY (`pid`) REFERENCES `db`.`p` (`id`) ON DELETE RESTRICT)")
	for _, c := range ch {
		if strings.Contains(c.Kind, "fk") {
			t.Errorf("same foreign key with another generated name should match: %+v", c)
		}
	}
}

func TestLargeTable(t *testing.T) {
	s := ParseTable("CREATE TABLE big (a bigint NOT NULL)", "")
	g := ParseTable("CREATE TABLE big (a int)", "")
	ch := DiffTables(s, g, Sizes{"big": {Rows: 5_000_000, Bytes: 2 << 30}})
	c := find(t, ch, "large_table")
	if !strings.Contains(c.Text, "~5,000,000 rows, 2.0 GB") {
		t.Errorf("unexpected text: %s", c.Text)
	}
	cmd := OnlineSchemaChangeCommand(TableDiff{Name: "big", Changes: ch}, "shop")
	if !strings.HasPrefix(cmd, "pt-online-schema-change --alter 'MODIFY COLUMN `a` bigint NOT NULL' \\\n  D=shop,t=big --dry-run") {
		t.Errorf("unexpected command: %s", cmd)
	}
}

func TestParseDumpNotes(t *testing.T) {
	tables, notes := ParseDump("CREATE TABLE a (x int); CREATE TABLE b LIKE a; create table A (y int);")
	if len(tables) != 1 || tables["A"] == "" || len(notes) != 2 {
		t.Errorf("tables %v notes %v", tables, notes)
	}
}

func TestStripComments(t *testing.T) {
	got := StripComments("a -- x\nb /* c */ d /*!50100 PARTITION BY x */ 'it''s -- not' # end")
	if !strings.Contains(got, "PARTITION BY x") || strings.Contains(got, "c */") || !strings.Contains(got, "'it''s -- not'") {
		t.Errorf("got %q", got)
	}
}

func TestCleanPastedDDL(t *testing.T) {
	in := "mysql> show create table t\\G\n*************************** 1. row ***************************\n       Table: t\nCreate Table: CREATE TABLE `t` (\n  `a` int\n) ENGINE=InnoDB\n1 row in set (0.00 sec)\n\nCREATE TABLE u (b int)"
	tables, _ := ParseDump(CleanPastedDDL(in))
	if len(tables) != 2 {
		t.Errorf("expected 2 tables, got %v", tables)
	}
}

func TestNamesOtherDatabase(t *testing.T) {
	for stmt, want := range map[string]bool{
		"ALTER TABLE `t` ADD COLUMN `a` int":                           false,
		"ALTER TABLE `t` ADD FOREIGN KEY (a) REFERENCES `db`.`p` (id)": true,
		"ALTER TABLE t ADD FOREIGN KEY (a) REFERENCES db.p (id)":       true,
		"ALTER TABLE t ADD COLUMN a decimal(5,2) DEFAULT 1.5":          false,
		"ALTER TABLE t ADD COLUMN a varchar(9) DEFAULT 'x.y'":          false,
	} {
		if got := NamesOtherDatabase(stmt); got != want {
			t.Errorf("%s: got %v, want %v", stmt, got, want)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	b := []string{"1", "2", "3", "x", "5", "6", "7", "8", "9", "10"}
	want := "--- a\n+++ b\n@@ -2,5 +2,5 @@\n 2\n 3\n-4\n+x\n 5\n 6\n@@ -8,2 +8,3 @@\n 8\n 9\n+10"
	if got := UnifiedDiff(a, b, "a", "b", 2); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if UnifiedDiff(a, a, "a", "b", 2) != "" {
		t.Error("equal input should give no diff")
	}
}

func TestSnapshots(t *testing.T) {
	st := SnapshotStore{Dir: t.TempDir()}
	id, err := st.Save("base", "label", "db", map[string]string{"t": "CREATE TABLE t (a int)"}, nil, "me")
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Tables != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	snap, err := st.Load(id)
	if err != nil || snap.Tables["t"] == "" || snap.Database != "db" {
		t.Fatalf("load %v %v", snap, err)
	}
	if _, err := st.Load("../../etc/passwd"); err == nil {
		t.Error("invalid ids must be rejected")
	}
	if err := st.Delete(id); err != nil {
		t.Fatal(err)
	}
}

func TestReportsRender(t *testing.T) {
	r := DiffSchemas(load(t, "source.sql"), load(t, "target.sql"), nil, "", "")
	in := ReportInput{Result: r, Script: r.MigrationScript(ScriptOptions{}), SourceLabel: "s <prod>", TargetLabel: "t"}
	h := HTMLReport(in)
	if !strings.Contains(h, "s &lt;prod&gt;") || !strings.Contains(h, "<code>") || !strings.HasSuffix(h, "</html>\n") {
		t.Error("html report is not escaped or incomplete")
	}
	if _, err := JSONReport(in); err != nil {
		t.Fatal(err)
	}
}

// Found while testing: MySQL makes primary key columns NOT NULL, so hand-written DDL that leaves it out matches a dump.
func TestPrimaryKeyColumnsAreNotNull(t *testing.T) {
	for _, src := range []string{"CREATE TABLE s (k varchar(64) PRIMARY KEY, v json)",
		"CREATE TABLE s (k varchar(64), v json, PRIMARY KEY (k))"} {
		if ch := diffOne(t, src, "CREATE TABLE `s` (`k` varchar(64) NOT NULL, `v` json, PRIMARY KEY (`k`))"); len(ch) != 0 {
			t.Errorf("%s: expected no changes, got %+v", src, ch)
		}
	}
}

func TestMultiDatabaseScript(t *testing.T) {
	diff := func(src, tgt map[string]string, db string) ReportInput {
		return ReportInput{Result: DiffSchemas(src, tgt, nil, db, db), Database: db}
	}
	m := MultiReport{SourceLabel: "s", TargetLabel: "t", Databases: []DatabaseReport{
		{Name: "app", Status: DBDiffers, Input: diff(map[string]string{"t": "CREATE TABLE t (a int, b int)"},
			map[string]string{"t": "CREATE TABLE t (a int, c varchar(10))"}, "app")},
		{Name: "same", Status: DBIdentical, Input: diff(nil, nil, "same")},
		{Name: "empty", Status: DBOnlySource, CreateDatabase: "CREATE DATABASE IF NOT EXISTS `empty`", Input: diff(nil, nil, "empty")},
		{Name: "old", Status: DBOnlyTarget},
	}}
	script := m.Script()
	for _, want := range []string{"USE `app`;", "ADD COLUMN `b` int", "--   DROP COLUMN `c`;", "-- --include-drops to include them.",
		"CREATE DATABASE IF NOT EXISTS `empty`;", "-- DROP DATABASE `old`;"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "`same`") {
		t.Errorf("identical databases do not belong in the script:\n%s", script)
	}
	if c := m.Summary(); c.Differ != 1 || c.OnlySource != 1 || c.OnlyTarget != 1 || c.Identical != 1 || !m.HasDifferences() {
		t.Errorf("summary %+v", c)
	}
	for _, out := range []string{MultiTextReport(m), MultiMarkdownReport(m), MultiHTMLReport(m)} {
		if !strings.Contains(out, "only on the target") {
			t.Error("report lacks the database overview")
		}
	}
	if _, err := MultiJSONReport(m); err != nil {
		t.Fatal(err)
	}
}
