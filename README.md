# pt-schema-diff

`pt-schema-diff` compares the tables of two MySQL schemas and writes the SQL that makes the **target** match the **source**. It rates every change for data-loss risk and for how MySQL 8.0+ runs it.


```
pt-schema-diff h=prod-db,u=reader,D=shop h=staging-db,D=shop --ask-pass
```

`pt-schema-diff` only **reads** the source and target. It never runs anything on them.

## What it does

- **Compares** tables, columns, indexes (primary, unique, full-text, spatial, functional, invisible), foreign keys, CHECK constraints and table options (engine, charset/collation, row format, comment, compression, stats options).
- **Tells apart changes that are only written differently.** It normalizes integer display widths, `utf8` → `utf8mb3`, `now()` → `CURRENT_TIMESTAMP`, implied charsets and collations, generated index/constraint names (`orders_ibfk_1`), quoted vs unquoted names, and versioned comments (`/*!50100 ... */`).
- **Detects renames.** A column with the same definition that exists only in the target is renamed with `CHANGE COLUMN`, which keeps its data. An index with the same definition is renamed with `RENAME INDEX`.
- **Rates each change:**
  - Severity: `CRITICAL` means data can be lost, `WARNING` means it can fail or block writes, `SUGGESTION` is worth a look, `OK` is safe.
  - How MySQL runs it: `instant` (metadata only), `online` (reads and writes continue), `online, rebuilds table`, or `blocks writes` (table copy).
  - A ready-made "check first" query where one helps, for example duplicate values before adding a `UNIQUE` index, or `MAX(CHAR_LENGTH())` before shrinking a `VARCHAR`.
- **Writes a migration script in phases that always run in order:** drop changed or removed foreign keys → create new tables → alter tables → drop tables → add foreign keys. Statements that delete data (`DROP TABLE`, `DROP COLUMN`) stay **commented out** unless `--include-drops` is given.
- **Suggests `pt-online-schema-change` for large tables.** When an ALTER would rebuild or copy a table of 1 GB+ or 1M+ rows, it shows a ready `pt-online-schema-change` command.
- **Tests the script** (`--verify`) on a sandbox server. It builds the target's tables in a scratch database, runs the script there, diffs the result against the source again, and drops the scratch database.
- **Snapshots:** save a schema now and compare against it later to track drift.
- **Reports** in text, SQL, Markdown, HTML (self-contained and printable to PDF from a browser) or JSON (for CI and other tools).

Not compared: views, routines, triggers, events. Partitioning and column-order differences are reported but not scripted.

## Install

```
make build            # bin/pt-schema-diff
make cross            # linux/darwin × amd64/arm64 static binaries
```

Requires Go 1.22+. The only dependency is `github.com/go-sql-driver/mysql`, the same driver the Go tools in Percona Toolkit use.

## Usage

```
pt-schema-diff [OPTIONS] SOURCE TARGET
pt-schema-diff [OPTIONS] h=SOURCE_HOST h=TARGET_HOST      # every database (no D=)
pt-schema-diff [OPTIONS] --save-snapshot NAME SCHEMA
pt-schema-diff [OPTIONS] --list-snapshots
pt-schema-diff [OPTIONS] --delete-snapshot ID
pt-schema-diff [OPTIONS] --list-databases DSN
```

`SOURCE`, `TARGET` and `SCHEMA` can each be:

| Form | Meaning |
|---|---|
| `h=host,P=3306,u=user,p=pass,S=socket,D=db,A=charset,F=my.cnf` | A live server ([Percona Toolkit DSN](https://docs.percona.com/percona-toolkit/dsn_data_source_name_specifications.html)). `D` is required. Values left out of the target DSN are taken from the source DSN. |
| `file.sql` or `-` | `mysqldump --no-data` output, `SHOW CREATE TABLE` output (including `\G` terminal output pasted from the mysql client), or hand-written `CREATE TABLE` statements. |
| `snapshot:ID`, `snapshot:latest` | A saved snapshot. |

### Every database of two servers

Leave `D=` out of **both** DSNs to compare every database (system schemas excluded):

```bash
pt-schema-diff h=db-primary,u=reader h=db-replica --ask-pass
pt-schema-diff h=src,u=reader h=dst --databases app,crm          # only these
pt-schema-diff h=src,u=reader h=dst --ignore-databases tmp,test  # all but these
```

- **Report:** an overview (identical / differs / only on the source / only on the target / error), then the full findings of every database that differs.
- **Script:** one script with a `USE` line per database.
  - A database that exists only on the source gets `CREATE DATABASE IF NOT EXISTS` with its character set, plus its tables.
  - A database that exists only on the target is reported, and its `DROP DATABASE` is always left as a comment.
- **Other options:** `--verify`, `--tables`, `--include-drops` and the formats work per database.
- **Errors:** a database that can't be read (for example, no privileges) is reported as an error, and the others are still compared. The exit status is then 1.

### Examples

```bash
# Production vs staging on another host, same credentials
pt-schema-diff h=prod,u=reader,D=shop h=staging,D=shop --ask-pass

# Only the migration script, with drops, into a file
pt-schema-diff h=prod,D=shop h=staging,D=shop --format sql --include-drops --output migrate.sql

# Test the script on a sandbox server before running it anywhere
pt-schema-diff h=prod,D=shop h=staging,D=shop --verify h=sandbox,u=sandbox,p=...

# The CREATE TABLE file in git vs a live server, as a CI check (exit status 2 = drift)
pt-schema-diff schema/shop.sql h=db,u=ci,D=shop --format json > drift.json

# Track drift on one server
pt-schema-diff --save-snapshot "before release 42" h=db,u=reader,D=shop
pt-schema-diff snapshot:latest h=db,u=reader,D=shop --format html --output drift.html

# A few tables only, with the CREATE TABLE diff of each
pt-schema-diff a.sql b.sql --tables orders,customers --ddl-diff
```

### Options

| Option | Description |
|---|---|
| `--format text\|sql\|markdown\|html\|json` | Output format (default `text`). |
| `--output FILE` | Write the report to a file. |
| `--script FILE` | Also write the migration script to a file. |
| `--include-drops` | Include `DROP TABLE` / `DROP COLUMN` instead of commenting them out. |
| `--use-database NAME`, `--no-use` | Database for the script's `USE` line (default: the target's `D`), or no `USE` line. |
| `--tables LIST`, `--ignore-tables LIST` | Compare only, or skip, these tables. |
| `--show-same` | Also list identical tables. |
| `--ddl-diff` | Show a unified diff of each changed table's `CREATE TABLE`. |
| `--verify DSN` | Test the script on a sandbox server (see below). |
| `--title TEXT` | Title of Markdown/HTML reports. |
| `--snapshot-dir DIR` | Snapshot directory (default `$PT_SCHEMA_DIFF_SNAPSHOT_DIR` or `~/.pt-schema-diff/snapshots`). |
| `--host`, `--port`, `--user`, `--password`, `--socket`, `--charset` | Defaults for DSNs. `MYSQL_PWD` is used when no password is given. |
| `--defaults-file FILE` | Read `user`, `password`, `host`, `port`, `socket` from the `[client]` section. |
| `--ask-pass` | Prompt for the password. |
| `--ssl-mode disabled\|preferred\|required\|verify_identity` | TLS for connections (default `preferred`). |
| `--version`, `--help` | |

Options can go before or after the positional arguments.

### Exit status

Same convention as `pt-table-sync`:

| Status | Meaning |
|---|---|
| 0 | The schemas match. |
| 1 | Error. |
| 2 | Differences found. |
| 3 | Differences found, and the `--verify` test of the script failed. |

### Privileges

- **Source and target:** `SELECT` on the schema (for `SHOW CREATE TABLE`) is enough. `information_schema.TABLES` supplies approximate table sizes.
- **`--verify` sandbox:** the login needs to create and drop databases named `pt_schema_diff_*`:

  ```sql
  GRANT ALL ON `pt\_schema\_diff\_%`.* TO 'sandbox'@'%';
  ```

  Use the same MySQL version as the target. The test runs on empty tables, so it proves the SQL and the end result, not the run time or data problems such as duplicates, NULLs or values that are too long. The warnings point at those.

  Before it runs anything, `--verify` refuses statements other than `CREATE TABLE` / `ALTER TABLE`, statements that name another database, and table options that reach outside the database (`DATA DIRECTORY`, `TABLESPACE`, `FEDERATED`, …).

## How MySQL runs a change

| Label | Algorithm | Meaning |
|---|---|---|
| instant | `INSTANT` (or metadata-only `INPLACE`) | Only the table definition changes. |
| online | `INPLACE` | Reads and writes continue; index builds read the whole table. |
| online, rebuilds table | `INPLACE` with rebuild | Reads and writes continue, but the table is rewritten (time and disk space). |
| blocks writes | `COPY` | The table is copied or checked row by row; writes wait until it finishes. |

Each table is labeled with its most expensive change.

## Layout

```
cmd/pt-schema-diff/   CLI: options, Percona Toolkit DSNs, --ask-pass, defaults files, exit status
schemadiff/           library (no CLI code), usable from other Go programs
  text.go             comments, literals, quoting, balanced parentheses, pasted-DDL cleanup
  parse.go            CREATE TABLE → columns, indexes, foreign keys, CHECKs, options
  risk.go             severity and ALTER algorithm of column changes
  diff.go             table and schema comparison
  script.go           migration script, pt-online-schema-change command, DDL diff
  udiff.go            unified diff (same output as Python's difflib)
  report.go           text, Markdown, HTML and JSON reports
  live.go             reading schemas from a server
  snapshot.go         saved schemas
  verify.go           testing the script on a sandbox server
testdata/             fixtures and golden outputs
```

### Tests

```
make test                                   # unit and golden tests
make test-live DSN=S=/tmp/mysql.sock,u=root # also against a real server
make golden                                 # rewrite golden files after an intended change
```

The golden outputs in `testdata/golden` were checked against the original Python implementation. On the same fixtures, both produce identical findings, clauses, DDL diffs and migration scripts.

## Percona Toolkit integration

The tool already follows Toolkit conventions, so adding it is mostly a move:

- **Name and CLI:** `pt-` prefix, Toolkit DSNs (`h,P,u,p,S,D,A,F`, with later DSNs inheriting from the first), `--ask-pass`, `--defaults-file`, `--version` output, and `pt-table-sync`-style exit status.
- **Code layout:** copy `cmd/pt-schema-diff` to `src/go/pt-schema-diff/` and `schemadiff/` to `src/go/pt-schema-diff/schemadiff/` (or to the Toolkit's shared `src/go/lib/`), then change the import path from `github.com/Percona-Lab/pt-schema-diff/schemadiff` to the Toolkit module path. The library has no global state apart from the replaceable `schemadiff.Now` clock.
- **Build metadata:** `main.Version`, `main.Build` and `main.Commit` are set with `-ldflags -X`, the way the Toolkit's Go Makefile does for its other Go tools.
- **Dependencies:** only `go-sql-driver/mysql`, which the Toolkit already vendors.
- **Still to do:** a `docs/pt-schema-diff.rst` page in the Toolkit's documentation style (this README is the source), adding the tool to the Toolkit's `Makefile` and packaging lists, and Toolkit-style `--config` file support if the maintainers want it.

## Differences from the Python version

- A command-line tool instead of a Streamlit tab. Connecting to servers, choosing a database, pasting DDL, saving or deleting snapshots, and the test button are now positional arguments and options.
- PDF output was dropped. The HTML report is self-contained and prints cleanly to PDF from any browser.
- The `--verify` scratch databases are named `pt_schema_diff_*` instead of `sqlsandbox_*`.
- Escaped quotes inside string literals (`'a\'b'`) are handled correctly when finding balanced parentheses.
- Added: `--tables` / `--ignore-tables`, `snapshot:latest`, JSON output, and the exit status for CI.

## License

GPL-2.0, the same license as Percona Toolkit. See [LICENSE](LICENSE).
