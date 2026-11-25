package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDSN(t *testing.T) {
	d, err := parseDSN("h=db1,P=3307,u=me,p=a=b,D=shop")
	if err != nil || d.Host != "db1" || d.Port != "3307" || d.User != "me" || d.Password != "a=b" || !d.passwordSet || d.Database != "shop" {
		t.Fatalf("%+v %v", d, err)
	}
	t2, _ := parseDSN("D=staging")
	t2.fill(d)
	if t2.Host != "db1" || t2.User != "me" || t2.Database != "staging" {
		t.Errorf("target should inherit from source: %+v", t2)
	}
	if _, err := parseDSN("h=x,t=tbl"); err == nil {
		t.Error("t= should be rejected")
	}
	if _, err := parseDSN("x=1"); err == nil {
		t.Error("unknown keys should be rejected")
	}
}

func TestDefaultsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my.cnf")
	os.WriteFile(path, []byte("[mysqld]\nuser=nope\n[client]\nuser = reader\npassword=\"s3cret\"\nport=3310\n"), 0o600)
	d, err := readDefaultsFile(path)
	if err != nil || d.User != "reader" || d.Password != "s3cret" || d.Port != "3310" {
		t.Errorf("%+v %v", d, err)
	}
}

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestExitCodes(t *testing.T) {
	src, tgt := "../../testdata/source.sql", "../../testdata/target.sql"
	if code, _, stderr := runCLI(src, src); code != exitSame {
		t.Errorf("identical schemas: exit %d %s", code, stderr)
	}
	code, out, _ := runCLI("--format", "sql", src, tgt)
	if code != exitDifferences || !strings.Contains(out, "ALTER TABLE `customers`") {
		t.Errorf("differences: exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI(src); code != exitError {
		t.Errorf("missing target: exit %d", code)
	}
	if code, _, _ := runCLI("--format", "pdf", src, tgt); code != exitError {
		t.Errorf("bad format: exit %d", code)
	}
}

func TestOptionsAfterArguments(t *testing.T) {
	code, out, stderr := runCLI("../../testdata/source.sql", "../../testdata/target.sql", "--format", "json", "--tables", "orders")
	if code != exitDifferences {
		t.Fatalf("exit %d %s", code, stderr)
	}
	var doc struct {
		Tables []struct{ Name string } `json:"tables"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tables) != 1 || doc.Tables[0].Name != "orders" {
		t.Errorf("--tables should keep only orders: %+v", doc.Tables)
	}
}

// TestLive runs against a real server when PT_SCHEMA_DIFF_TEST_DSN is set, e.g. S=/tmp/mysql.sock,u=root. It creates
// and drops the databases pt_schema_diff_test_src and pt_schema_diff_test_tgt.
func TestLive(t *testing.T) {
	base := os.Getenv("PT_SCHEMA_DIFF_TEST_DSN")
	if base == "" {
		t.Skip("set PT_SCHEMA_DIFF_TEST_DSN to run against a MySQL server")
	}
	d, err := parseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	db, err := d.open("preferred")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn := func(stmts ...string) {
		t.Helper()
		for _, s := range stmts {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
	}
	conn("DROP DATABASE IF EXISTS pt_schema_diff_test_src", "DROP DATABASE IF EXISTS pt_schema_diff_test_tgt",
		"CREATE DATABASE pt_schema_diff_test_src", "CREATE DATABASE pt_schema_diff_test_tgt")
	defer conn("DROP DATABASE pt_schema_diff_test_src", "DROP DATABASE pt_schema_diff_test_tgt")
	conn("CREATE TABLE pt_schema_diff_test_src.p (id int PRIMARY KEY, name varchar(50) NOT NULL DEFAULT '')",
		"CREATE TABLE pt_schema_diff_test_src.c (id int PRIMARY KEY, pid int, KEY (pid), FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE TABLE pt_schema_diff_test_tgt.p (id int PRIMARY KEY, name varchar(20), extra int)",
		"CREATE TABLE pt_schema_diff_test_tgt.gone (id int PRIMARY KEY)")
	code, out, stderr := runCLI(base+",D=pt_schema_diff_test_src", "D=pt_schema_diff_test_tgt", "--verify", base,
		"--include-drops", "--format", "json")
	if code != exitDifferences {
		t.Fatalf("exit %d: %s\n%s", code, stderr, out)
	}
	var doc struct {
		Verify struct {
			OK    bool
			Error string
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Verify.OK {
		t.Errorf("script test failed: %s\n%s", doc.Verify.Error, out)
	}
	var leftover sql.NullString
	db.QueryRow("SELECT MAX(SCHEMA_NAME) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'pt\\_schema\\_diff\\_%' AND SCHEMA_NAME NOT LIKE 'pt\\_schema\\_diff\\_test%'").Scan(&leftover)
	if leftover.Valid {
		t.Errorf("scratch database left behind: %s", leftover.String)
	}
}

func TestServerWide(t *testing.T) {
	if !serverWide("h=db1,u=me") || serverWide("h=db1,D=x") || serverWide("a.sql") || serverWide("snapshot:latest") {
		t.Error("serverWide detection")
	}
	code, _, stderr := runCLI("h=127.0.0.1,u=x,D=a", "h=127.0.0.2")
	if code != exitError || !strings.Contains(stderr, "leave it out on both") {
		t.Errorf("mixed D=: exit %d %s", code, stderr)
	}
}

func TestArgumentErrors(t *testing.T) {
	src := "../../testdata/source.sql"
	for args, want := range map[string]string{
		"--verify":               "--verify needs the DSN of a sandbox server",
		"--verify --format json": "--verify needs the DSN of a sandbox server",
		"--bogus":                "unknown option --bogus",
		"--format":               "option --format needs a value",
	} {
		code, out, stderr := runCLI(append([]string{src, src}, strings.Fields(args)...)...)
		if code != exitError || !strings.Contains(stderr, want) || strings.Contains(stderr, "Options:") || out != "" {
			t.Errorf("%s: exit %d, stderr %q", args, code, stderr)
		}
	}
	if code, out, _ := runCLI("-h"); code != exitSame || !strings.Contains(out, "Usage:") {
		t.Errorf("-h should print the help: exit %d", code)
	}
}
