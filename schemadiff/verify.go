package schemadiff

// Testing the migration script on a sandbox server: the target's tables are built in a scratch database, the script
// runs there, and the result is compared with the source again. Nothing runs on the source or target servers.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// VerifyResult is the outcome of testing the script.
type VerifyResult struct {
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	Statement string   `json:"statement,omitempty"` // the statement that failed
	Remaining []string `json:"remaining"`           // differences the script should have fixed but didn't
	Expected  []string `json:"expected"`            // differences left on purpose (excluded drops, not scripted)
	Version   string   `json:"version"`
	Count     int      `json:"count"` // statements in the script
	Database  string   `json:"scratch_database"`
}

var (
	reScratchDropTable = regexp.MustCompile("(?is)^\\s*DROP\\s+TABLE\\s+`(?:[^`]|``)+`\\s*;?\\s*$")
	reSandboxAllowed   = regexp.MustCompile(`(?i)^\s*(CREATE\s+TABLE|ALTER\s+TABLE)\b`)
	reSandboxForbidden = regexp.MustCompile(`(?i)\b(DATA\s+DIRECTORY|INDEX\s+DIRECTORY|TABLESPACE|CONNECTION\s*=|ENGINE\s*=\s*(?:FEDERATED|CONNECT|MERGE|MRG_MYISAM))`)
)

// statementProblem says why a statement may not run on the sandbox server, or "" when it may.
func statementProblem(stmt string) string {
	if strings.Contains(stmt, "/*!") {
		return "executable comments (/*! ... */) are not run"
	}
	code, _ := protect(StripComments(stmt), reTextLiteral)
	switch {
	case !reSandboxAllowed.MatchString(code):
		return "only CREATE TABLE and ALTER TABLE statements run on the sandbox server"
	case reSandboxForbidden.MatchString(code):
		return "table options that reach outside the database (directories, tablespaces, remote tables) are not run"
	case NamesOtherDatabase(stmt):
		return "it names a table in another database"
	}
	return ""
}

func mysqlErrorText(err error) string {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return fmt.Sprintf("MySQL error %d: %s", me.Number, me.Message)
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// ScratchPrefix starts the name of every scratch database the test creates (and drops).
const ScratchPrefix = "pt_schema_diff_"

// VerifyScript runs the migration on a scratch copy of the target on the sandbox server, then diffs the result with
// the source. sourceTables and sourceDB describe the source, as given to DiffSchemas.
func VerifyScript(ctx context.Context, sandbox *sql.DB, r Result, sourceTables map[string]string, sourceDB string, includeDrops bool) *VerifyResult {
	out := &VerifyResult{Remaining: []string{}, Expected: []string{}}
	var creates []string
	for _, t := range r.Tables {
		if t.Target != nil {
			creates = append(creates, CreateStatement(t.Target, true)+";")
		}
	}
	migration := r.ExecutableStatements(includeDrops)
	out.Count = len(migration)
	for _, stmt := range append(append([]string{}, creates...), migration...) {
		if reScratchDropTable.MatchString(stmt) {
			continue // drops a table of the scratch database
		}
		if p := statementProblem(stmt); p != "" {
			out.Error, out.Statement = "Not run on the sandbox server: "+p+".", stmt
			return out
		}
	}

	conn, err := sandbox.Conn(ctx)
	if err != nil {
		out.Error = mysqlErrorText(err)
		return out
	}
	defer conn.Close()
	if out.Version, err = ServerVersion(ctx, conn); err != nil {
		out.Error = mysqlErrorText(err)
		return out
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	scratch := ScratchPrefix + hex.EncodeToString(suffix)
	out.Database = scratch
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+Quote(scratch)); err != nil {
		out.Error = mysqlErrorText(err)
		return out
	}
	defer func() {
		// Drop the scratch database even when the caller's context was cancelled.
		_, _ = conn.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+Quote(scratch))
	}()

	run := func(stmt string) bool {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			out.Error, out.Statement = mysqlErrorText(err), stmt
			return false
		}
		return true
	}
	if !run("USE "+Quote(scratch)) || !run("SET SESSION foreign_key_checks = 0") { // the target's tables, in any order
		return out
	}
	for _, stmt := range creates {
		if !run(stmt) {
			return out
		}
	}
	if !run("SET SESSION foreign_key_checks = 1") {
		return out
	}
	for _, stmt := range migration {
		if !run(stmt) {
			return out
		}
	}
	after, _, err := FetchSchema(ctx, conn, scratch)
	if err != nil {
		out.Error = mysqlErrorText(err)
		return out
	}
	again := DiffSchemas(sourceTables, after, nil, sourceDB, scratch)
	for _, e := range again.Tables {
		for _, c := range e.Changes {
			line := fmt.Sprintf("`%s`: %s", e.Name, c.Text)
			if c.Kind == "column_order" || c.Kind == "partition" || c.Kind == "large_table" || (c.DataLoss && !includeDrops) {
				out.Expected = append(out.Expected, line)
			} else {
				out.Remaining = append(out.Remaining, line)
			}
		}
	}
	out.OK = len(out.Remaining) == 0
	return out
}
