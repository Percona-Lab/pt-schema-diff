// pt-schema-diff compares two MySQL schemas and writes the SQL that makes the target match the source, rating every
// change for data-loss risk and for how MySQL 8.0 runs it (instant, online, rebuild, or blocking copy).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/Percona-Lab/pt-schema-diff/schemadiff"
)

const toolName = "pt-schema-diff"

// Set with -ldflags "-X main.Version=... -X main.Commit=... -X main.Build=...", as for the other toolkit tools.
var (
	Version = "0.1.0"
	Commit  = "unknown"
	Build   = "unknown"
)

// Exit statuses, like pt-table-sync: 0 the schemas match, 1 an error, 2 differences found, 3 differences found and
// the --verify test of the script failed.
const (
	exitSame        = 0
	exitError       = 1
	exitDifferences = 2
	exitVerifyFail  = 3
)

type options struct {
	format, output, scriptFile, useDatabase, title   string
	includeDrops, noUse, showSame, ddlDiff           bool
	tables, ignoreTables                             string
	databases, ignoreDatabases                       string
	verify                                           string
	snapshotDir, saveSnapshot, deleteSnapshot        string
	listSnapshots, listDatabases, version, help      bool
	host, port, user, password, socket, defaultsFile string
	charset, sslMode                                 string
	askPass, passwordSet                             bool
}

const usageText = `Usage:
  pt-schema-diff [OPTIONS] SOURCE TARGET
  pt-schema-diff [OPTIONS] h=SOURCE_HOST h=TARGET_HOST        (every database)
  pt-schema-diff [OPTIONS] --save-snapshot NAME SCHEMA
  pt-schema-diff [OPTIONS] --list-snapshots
  pt-schema-diff [OPTIONS] --delete-snapshot ID
  pt-schema-diff [OPTIONS] --list-databases DSN

Compares the tables of two MySQL schemas and prints the SQL that makes TARGET match SOURCE.
SOURCE is the schema to match (e.g. production); TARGET is the one the script changes (e.g. staging).
Every change is rated for data loss and for how MySQL 8.0 runs it. pt-schema-diff only reads
SOURCE and TARGET; it never changes them.

SOURCE, TARGET and SCHEMA are each one of:
  h=host,P=3306,u=user,D=db   a database on a live server (Percona Toolkit DSN). Values missing from
                              TARGET are taken from SOURCE. Keys: h P u p S D A F.
                              Leave D= out of both to compare every database of the two servers
                              (system schemas excluded; narrow with --databases/--ignore-databases).
  FILE.sql                    mysqldump --no-data output, SHOW CREATE TABLE output or CREATE TABLE
                              statements ("-" reads standard input)
  snapshot:ID                 a saved snapshot (snapshot:latest is the newest)

Options:
`

const usageFooter = `
Exit status: 0 the schemas match; 1 error; 2 differences found; 3 differences found and the
--verify test failed.

Compared: tables, columns, indexes, foreign keys, CHECK constraints and table options. Views,
routines, triggers and events are not. Partitioning and column order differences are reported but
not scripted. Statements that delete data (DROP TABLE, DROP COLUMN) stay commented out unless
--include-drops is given.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func newFlagSet(o *options, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.format, "format", "text", "Output `format`: text, sql, markdown, html, json")
	fs.StringVar(&o.output, "output", "", "Write the report to `FILE` instead of standard output")
	fs.StringVar(&o.scriptFile, "script", "", "Also write the migration script to `FILE`")
	fs.BoolVar(&o.includeDrops, "include-drops", false, "Include statements that delete data (DROP TABLE, DROP COLUMN) instead of commenting them out")
	fs.StringVar(&o.useDatabase, "use-database", "", "`Database` for the script's USE line (default: the target's database)")
	fs.BoolVar(&o.noUse, "no-use", false, "Leave the USE line out of the script")
	fs.BoolVar(&o.showSame, "show-same", false, "Also list identical tables")
	fs.BoolVar(&o.ddlDiff, "ddl-diff", false, "Show a unified diff of the CREATE TABLE of each changed table")
	fs.StringVar(&o.tables, "tables", "", "Compare only these tables (comma-separated `list`)")
	fs.StringVar(&o.ignoreTables, "ignore-tables", "", "Do not compare these tables (comma-separated `list`)")
	fs.StringVar(&o.databases, "databases", "", "Without D=: compare only these databases (comma-separated `list`)")
	fs.StringVar(&o.ignoreDatabases, "ignore-databases", "", "Without D=: do not compare these databases (comma-separated `list`)")
	fs.StringVar(&o.verify, "verify", "", "Test the script on a sandbox server `DSN`: build the target's tables in a scratch database\n"+
		"(named "+schemadiff.ScratchPrefix+"...), run the script there, and check the result matches the source.\n"+
		"Needs CREATE/DROP on "+schemadiff.ScratchPrefix+"% databases. Use the same MySQL version as the target.")
	fs.StringVar(&o.title, "title", "", "Report title (markdown, html)")
	fs.StringVar(&o.snapshotDir, "snapshot-dir", "", "Snapshot `directory` (default $PT_SCHEMA_DIFF_SNAPSHOT_DIR or ~/.pt-schema-diff/snapshots)")
	fs.StringVar(&o.saveSnapshot, "save-snapshot", "", "Save SCHEMA as a snapshot called `NAME`, to compare against later")
	fs.BoolVar(&o.listSnapshots, "list-snapshots", false, "List saved snapshots")
	fs.StringVar(&o.deleteSnapshot, "delete-snapshot", "", "Delete the snapshot with this `ID`")
	fs.BoolVar(&o.listDatabases, "list-databases", false, "List the databases of the server in DSN")
	fs.StringVar(&o.host, "host", "", "Default `host` for DSNs")
	fs.StringVar(&o.port, "port", "", "Default `port` for DSNs")
	fs.StringVar(&o.user, "user", "", "Default `user` for DSNs")
	fs.Func("password", "Default `password` for DSNs (or set MYSQL_PWD)", func(v string) error {
		o.password, o.passwordSet = v, true
		return nil
	})
	fs.StringVar(&o.socket, "socket", "", "Default `socket` for DSNs")
	fs.StringVar(&o.defaultsFile, "defaults-file", "", "Read user, password, host, port and socket from the [client] section of this `file`")
	fs.StringVar(&o.charset, "charset", "", "Default connection `charset` for DSNs")
	fs.StringVar(&o.sslMode, "ssl-mode", "preferred", "TLS `mode`: disabled, preferred, required, verify_identity")
	fs.BoolVar(&o.askPass, "ask-pass", false, "Prompt for the password")
	fs.BoolVar(&o.version, "version", false, "Show the version and exit")
	fs.BoolVar(&o.help, "help", false, "Show this help and exit")
	fs.Usage = func() {
		out := fs.Output()
		var defaults strings.Builder
		fs.SetOutput(&defaults)
		fs.PrintDefaults()
		fs.SetOutput(out)
		// Toolkit tools spell long options with two dashes (the flag package accepts both).
		fmt.Fprint(out, usageText+strings.ReplaceAll("\n"+defaults.String(), "\n  -", "\n  --")[1:]+usageFooter)
	}
	return fs
}

// parseArgs parses options given anywhere on the command line, before or after the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		if rest[0] == "--" {
			return append(positional, rest[1:]...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

type cli struct {
	o              options
	stdout, stderr io.Writer
	ctx            context.Context
	defaults       dsn
	firstDSN       *dsn
}

func run(args []string, stdout, stderr io.Writer) int {
	c := &cli{stdout: stdout, stderr: stderr}
	fs := newFlagSet(&c.o, stderr)
	positional, err := parseArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitSame
	}
	if err != nil {
		return exitError
	}
	if c.o.help {
		fs.SetOutput(stdout)
		fs.Usage()
		return exitSame
	}
	if c.o.version {
		fmt.Fprintf(stdout, "%s %s\nBuild: %s using %s\nCommit: %s\n", toolName, Version, Build, runtime.Version(), Commit)
		return exitSame
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c.ctx = ctx
	code, err := c.dispatch(positional)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", toolName, err)
		return exitError
	}
	return code
}

func (c *cli) dispatch(positional []string) (int, error) {
	c.defaults = dsn{Host: c.o.host, Port: c.o.port, User: c.o.user, Password: c.o.password, passwordSet: c.o.passwordSet,
		Socket: c.o.socket, DefaultsFile: c.o.defaultsFile, Charset: c.o.charset}
	if c.o.askPass {
		pw, err := askPass("Enter password: ")
		if err != nil {
			return exitError, err
		}
		c.defaults.Password, c.defaults.passwordSet = pw, true
	}
	switch {
	case c.o.listSnapshots:
		return c.listSnapshotsCmd()
	case c.o.deleteSnapshot != "":
		if err := c.store().Delete(c.o.deleteSnapshot); err != nil {
			return exitError, err
		}
		fmt.Fprintf(c.stdout, "Deleted snapshot %s.\n", c.o.deleteSnapshot)
		return exitSame, nil
	case c.o.listDatabases:
		if len(positional) != 1 {
			return exitError, fmt.Errorf("--list-databases takes one DSN")
		}
		return c.listDatabasesCmd(positional[0])
	case c.o.saveSnapshot != "":
		if len(positional) != 1 {
			return exitError, fmt.Errorf("--save-snapshot takes one SCHEMA (a DSN or a file)")
		}
		return c.saveSnapshotCmd(positional[0])
	}
	if len(positional) != 2 {
		return exitError, fmt.Errorf("expected SOURCE and TARGET (see --help)")
	}
	return c.diffCmd(positional[0], positional[1])
}

func (c *cli) store() schemadiff.SnapshotStore {
	dir := c.o.snapshotDir
	if dir == "" {
		dir = os.Getenv("PT_SCHEMA_DIFF_SNAPSHOT_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".pt-schema-diff", "snapshots")
	}
	return schemadiff.SnapshotStore{Dir: dir}
}

// side is one loaded schema.
type side struct {
	kind     string // live, file, snapshot
	label    string
	database string
	tables   map[string]string
	sizes    schemadiff.Sizes
	notes    []string
}

func (c *cli) resolveDSN(arg string) (dsn, error) {
	d, err := parseDSN(arg)
	if err != nil {
		return d, err
	}
	if c.firstDSN != nil {
		d.fill(*c.firstDSN) // like other toolkit tools, the first DSN provides defaults for the next ones
	}
	d.fill(c.defaults)
	if c.firstDSN == nil {
		first := d
		c.firstDSN = &first
	}
	return d, nil
}

func (c *cli) load(arg, role string) (*side, error) {
	switch {
	case strings.HasPrefix(arg, "snapshot:"):
		id := strings.TrimPrefix(arg, "snapshot:")
		st := c.store()
		if id == "latest" {
			list, err := st.List()
			if err != nil {
				return nil, err
			}
			if len(list) == 0 {
				return nil, fmt.Errorf("%s: no snapshots in %s", role, st.Dir)
			}
			id = list[0].ID
		}
		snap, err := st.Load(id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", role, err)
		}
		return &side{kind: "snapshot", label: fmt.Sprintf("snapshot %s (%s, %s)", snap.Name, snap.ID, strings.Replace(snap.Created, "T", " ", 1)),
			database: snap.Database, tables: snap.Tables, sizes: snap.Sizes}, nil
	case arg == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		return fileSide("standard input", string(data), role)
	}
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		data, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		return fileSide(arg, string(data), role)
	}
	if !looksLikeDSN(arg) {
		return nil, fmt.Errorf("%s %q is neither a file nor a DSN (h=host,D=db) nor snapshot:ID", role, arg)
	}
	d, err := c.resolveDSN(arg)
	if err != nil {
		return nil, err
	}
	if d.Database == "" {
		return nil, fmt.Errorf("%s DSN %q needs a database (D=name)", role, arg)
	}
	db, err := d.open(c.o.sslMode)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tables, sizes, err := schemadiff.FetchSchema(c.ctx, db, d.Database)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", role, d.label(), err)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("%s: `%s` on %s has no tables this login can see", role, d.Database, d.label())
	}
	return &side{kind: "live", label: d.label(), database: d.Database, tables: tables, sizes: sizes}, nil
}

func fileSide(name, text, role string) (*side, error) {
	tables, notes := schemadiff.ParseDump(schemadiff.CleanPastedDDL(text))
	if len(tables) == 0 {
		return nil, fmt.Errorf("%s: no CREATE TABLE statements found in %s", role, name)
	}
	for i, n := range notes {
		notes[i] = role + ": " + n
	}
	return &side{kind: "file", label: "file " + name, tables: tables, notes: notes}, nil
}

func splitList(s string) map[string]bool {
	out := map[string]bool{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out[strings.ToLower(schemadiff.Unquote(v))] = true
		}
	}
	return out
}

func (c *cli) filter(tables map[string]string) map[string]string {
	only, ignore := splitList(c.o.tables), splitList(c.o.ignoreTables)
	out := map[string]string{}
	for name, ddl := range tables {
		k := strings.ToLower(name)
		if (len(only) == 0 || only[k]) && !ignore[k] {
			out[name] = ddl
		}
	}
	return out
}

func (c *cli) diffCmd(srcArg, tgtArg string) (int, error) {
	format := strings.ToLower(c.o.format)
	switch format {
	case "text", "sql", "markdown", "md", "html", "json":
	default:
		return exitError, fmt.Errorf("invalid --format %q (text, sql, markdown, html, json)", c.o.format)
	}
	if sw, tw := serverWide(srcArg), serverWide(tgtArg); sw && tw {
		return c.multiDiffCmd(srcArg, tgtArg, format)
	} else if sw || tw {
		role := "source"
		if tw {
			role = "target"
		}
		return exitError, fmt.Errorf("the %s DSN has no database: give D= on both sides to compare one database, "+
			"or leave it out on both to compare every database", role)
	}
	src, err := c.load(srcArg, "source")
	if err != nil {
		return exitError, err
	}
	tgt, err := c.load(tgtArg, "target")
	if err != nil {
		return exitError, err
	}
	srcTables, tgtTables := c.filter(src.tables), c.filter(tgt.tables)
	result := schemadiff.DiffSchemas(srcTables, tgtTables, tgt.sizes, src.database, tgt.database)
	result.Notes = append(append(append([]string{}, src.notes...), tgt.notes...), result.Notes...)

	useDB := tgt.database
	if c.o.useDatabase != "" {
		useDB = c.o.useDatabase
	}
	if c.o.noUse {
		useDB = ""
	}
	script := result.MigrationScript(schemadiff.ScriptOptions{IncludeDrops: c.o.includeDrops, Database: useDB,
		SourceLabel: src.label, TargetLabel: tgt.label, Tool: toolName + " " + Version})

	var verify *schemadiff.VerifyResult
	if c.o.verify != "" {
		d, err := c.resolveDSN(c.o.verify)
		if err != nil {
			return exitError, fmt.Errorf("--verify: %w", err)
		}
		db, err := d.open(c.o.sslMode)
		if err != nil {
			return exitError, fmt.Errorf("--verify: %w", err)
		}
		verify = schemadiff.VerifyScript(c.ctx, db, result, srcTables, src.database, c.o.includeDrops)
		db.Close()
	}

	in := schemadiff.ReportInput{Result: result, Script: script, SourceLabel: src.label, TargetLabel: tgt.label,
		Database: tgt.database, IncludeDrops: c.o.includeDrops, ShowSame: c.o.showSame, DDLDiff: c.o.ddlDiff,
		Verify: verify, Title: c.o.title}
	var report []byte
	switch format {
	case "text":
		report = []byte(schemadiff.TextReport(in))
	case "sql":
		report = []byte(script)
	case "markdown", "md":
		report = []byte(schemadiff.MarkdownReport(in))
	case "html":
		report = []byte(schemadiff.HTMLReport(in))
	case "json":
		if report, err = schemadiff.JSONReport(in); err != nil {
			return exitError, err
		}
		report = append(report, '\n')
	}
	if err := c.writeOutput(report, script); err != nil {
		return exitError, err
	}
	if verify != nil && !verify.OK {
		fmt.Fprintf(c.stderr, "%s: script test: %s\n", toolName, verify.Text())
		return exitVerifyFail, nil
	}
	if result.HasDifferences() {
		return exitDifferences, nil
	}
	return exitSame, nil
}

func (c *cli) listDatabasesCmd(arg string) (int, error) {
	if !looksLikeDSN(arg) {
		return exitError, fmt.Errorf("%q is not a DSN (h=host,u=user)", arg)
	}
	d, err := c.resolveDSN(arg)
	if err != nil {
		return exitError, err
	}
	db, err := d.open(c.o.sslMode)
	if err != nil {
		return exitError, err
	}
	defer db.Close()
	dbs, err := schemadiff.ListDatabases(c.ctx, db)
	if err != nil {
		return exitError, err
	}
	for _, name := range dbs {
		fmt.Fprintln(c.stdout, name)
	}
	return exitSame, nil
}

func (c *cli) saveSnapshotCmd(arg string) (int, error) {
	if strings.HasPrefix(arg, "snapshot:") {
		return exitError, fmt.Errorf("SCHEMA is already a snapshot")
	}
	s, err := c.load(arg, "schema")
	if err != nil {
		return exitError, err
	}
	for _, n := range s.notes {
		fmt.Fprintf(c.stderr, "%s: %s\n", toolName, n)
	}
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	st := c.store()
	id, err := st.Save(c.o.saveSnapshot, s.label, s.database, c.filter(s.tables), s.sizes, who)
	if err != nil {
		return exitError, err
	}
	fmt.Fprintf(c.stdout, "Saved snapshot %s (%q, %d tables from %s) in %s\nCompare with it later as snapshot:%s\n",
		id, c.o.saveSnapshot, len(c.filter(s.tables)), s.label, st.Dir, id)
	return exitSame, nil
}

func (c *cli) listSnapshotsCmd() (int, error) {
	st := c.store()
	list, err := st.List()
	if err != nil {
		return exitError, err
	}
	if len(list) == 0 {
		fmt.Fprintf(c.stdout, "No snapshots in %s. Save one with --save-snapshot NAME SCHEMA.\n", st.Dir)
		return exitSame, nil
	}
	w := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tTABLES\tCREATED\tBY\tFROM")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", s.ID, s.Name, s.Tables, strings.Replace(s.Created, "T", " ", 1), s.CreatedBy, s.Label)
	}
	w.Flush()
	return exitSame, nil
}
