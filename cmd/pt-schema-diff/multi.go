package main

// Comparing every database of two servers: SOURCE and TARGET are DSNs without D=.

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Percona-Lab/pt-schema-diff/schemadiff"
)

// serverWide is true when arg is a live-server DSN without a database (and not a file of that name).
func serverWide(arg string) bool {
	if strings.HasPrefix(arg, "snapshot:") || arg == "-" || !looksLikeDSN(arg) {
		return false
	}
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		return false
	}
	d, err := parseDSN(arg)
	return err == nil && d.Database == ""
}

func listFilter(list string) func(string) bool {
	names := map[string]bool{}
	for _, v := range strings.Split(list, ",") {
		if v = strings.TrimSpace(schemadiff.Unquote(strings.TrimSpace(v))); v != "" {
			names[strings.ToLower(v)] = true
		}
	}
	return func(name string) bool { return names[strings.ToLower(name)] }
}

func (c *cli) multiDiffCmd(srcArg, tgtArg, format string) (int, error) {
	sd, err := c.resolveDSN(srcArg)
	if err != nil {
		return exitError, err
	}
	td, err := c.resolveDSN(tgtArg)
	if err != nil {
		return exitError, err
	}
	open := func(d dsn, role string) (*sql.DB, []string, error) {
		db, err := d.open(c.o.sslMode)
		if err != nil {
			return nil, nil, err
		}
		names, err := schemadiff.ListDatabases(c.ctx, db)
		if err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("%s %s: %w", role, d.label(), err)
		}
		return db, names, nil
	}
	sdb, srcNames, err := open(sd, "source")
	if err != nil {
		return exitError, err
	}
	defer sdb.Close()
	tdb, tgtNames, err := open(td, "target")
	if err != nil {
		return exitError, err
	}
	defer tdb.Close()

	only, ignore := listFilter(c.o.databases), listFilter(c.o.ignoreDatabases)
	keep := func(name string) bool { return (c.o.databases == "" || only(name)) && !ignore(name) }
	inSrc, inTgt := map[string]bool{}, map[string]bool{}
	var names []string
	for _, n := range srcNames {
		if keep(n) {
			inSrc[n] = true
			names = append(names, n)
		}
	}
	for _, n := range tgtNames {
		if keep(n) {
			if !inSrc[n] {
				names = append(names, n)
			}
			inTgt[n] = true
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return exitError, fmt.Errorf("no databases to compare (this login sees none, or --databases / --ignore-databases left none)")
	}

	var verifyDB *sql.DB
	if c.o.verify != "" {
		vd, err := c.resolveDSN(c.o.verify)
		if err != nil {
			return exitError, fmt.Errorf("--verify: %w", err)
		}
		if verifyDB, err = vd.open(c.o.sslMode); err != nil {
			return exitError, fmt.Errorf("--verify: %w", err)
		}
		defer verifyDB.Close()
	}

	label := func(d dsn, db string) string { d.Database = db; return d.label() }
	m := schemadiff.MultiReport{SourceLabel: sd.label(), TargetLabel: td.label(), IncludeDrops: c.o.includeDrops,
		ShowSame: c.o.showSame, Title: c.o.title, Tool: toolName + " " + Version}
	failed, verifyFailed := false, false
	for _, name := range names {
		r := schemadiff.DatabaseReport{Name: name}
		if !inSrc[name] {
			r.Status = schemadiff.DBOnlyTarget
			m.Databases = append(m.Databases, r)
			continue
		}
		srcTables, _, err := schemadiff.FetchSchema(c.ctx, sdb, name)
		if err != nil {
			r.Status, r.Error, failed = schemadiff.DBError, "source: "+err.Error(), true
			m.Databases = append(m.Databases, r)
			continue
		}
		tgtTables, tgtSizes, tgtDB := map[string]string{}, schemadiff.Sizes{}, ""
		if inTgt[name] {
			tgtDB = name
			if tgtTables, tgtSizes, err = schemadiff.FetchSchema(c.ctx, tdb, name); err != nil {
				r.Status, r.Error, failed = schemadiff.DBError, "target: "+err.Error(), true
				m.Databases = append(m.Databases, r)
				continue
			}
		} else {
			if r.CreateDatabase, err = schemadiff.CreateDatabaseStatement(c.ctx, sdb, name); err != nil {
				r.CreateDatabase = "CREATE DATABASE IF NOT EXISTS " + schemadiff.Quote(name)
			}
		}
		srcTables, tgtTables = c.filter(srcTables), c.filter(tgtTables)
		result := schemadiff.DiffSchemas(srcTables, tgtTables, tgtSizes, name, tgtDB)
		switch {
		case !inTgt[name]:
			r.Status = schemadiff.DBOnlySource
		case result.HasDifferences():
			r.Status = schemadiff.DBDiffers
		default:
			r.Status = schemadiff.DBIdentical
		}
		r.Input = schemadiff.ReportInput{Result: result, SourceLabel: label(sd, name), TargetLabel: label(td, name),
			Database: name, IncludeDrops: c.o.includeDrops, ShowSame: c.o.showSame, DDLDiff: c.o.ddlDiff}
		if verifyDB != nil && r.Status != schemadiff.DBIdentical && len(result.Tables) > 0 {
			r.Input.Verify = schemadiff.VerifyScript(c.ctx, verifyDB, result, srcTables, name, c.o.includeDrops)
			if !r.Input.Verify.OK {
				verifyFailed = true
				fmt.Fprintf(c.stderr, "%s: script test of %s: %s\n", toolName, schemadiff.Quote(name), r.Input.Verify.Text())
			}
		}
		m.Databases = append(m.Databases, r)
	}

	script := m.Script()
	var report []byte
	switch format {
	case "text":
		report = []byte(schemadiff.MultiTextReport(m))
	case "sql":
		report = []byte(script)
	case "markdown", "md":
		report = []byte(schemadiff.MultiMarkdownReport(m))
	case "html":
		report = []byte(schemadiff.MultiHTMLReport(m))
	case "json":
		if report, err = schemadiff.MultiJSONReport(m); err != nil {
			return exitError, err
		}
		report = append(report, '\n')
	}
	if err := c.writeOutput(report, script); err != nil {
		return exitError, err
	}
	for _, d := range m.Databases {
		if d.Status == schemadiff.DBError {
			fmt.Fprintf(c.stderr, "%s: could not compare %s: %s\n", toolName, schemadiff.Quote(d.Name), d.Error)
		}
	}
	switch {
	case failed:
		return exitError, nil
	case verifyFailed:
		return exitVerifyFail, nil
	case m.HasDifferences():
		return exitDifferences, nil
	}
	return exitSame, nil
}

// writeOutput writes the report (to --output or standard output) and the script (to --script, if given).
func (c *cli) writeOutput(report []byte, script string) error {
	if c.o.output != "" {
		if err := os.WriteFile(c.o.output, report, 0o644); err != nil {
			return err
		}
	} else if _, err := c.stdout.Write(report); err != nil {
		return err
	}
	if c.o.scriptFile != "" {
		return os.WriteFile(c.o.scriptFile, []byte(script), 0o644)
	}
	return nil
}
