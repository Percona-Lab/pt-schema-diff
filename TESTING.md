# Testing pt-schema-diff yourself

A hands-on walkthrough of every feature against throwaway databases, with the result to expect at each step. It takes about 20 minutes.

Everything here runs against a MySQL server you can throw data at. The examples use a local server reachable as `root` over `/tmp/mysql.sock` and as the test users over `127.0.0.1`. Adjust the DSNs if yours differs.

## 0. Build and set up

```bash
cd ~/Percona-Lab/SchemaDiff
make build test                 # bin/pt-schema-diff; all tests should print "ok"
./testing/setup.sh --big        # MYSQL_OPTS="-uroot -p" ./testing/setup.sh --big if root has a password
mkdir -p /tmp/sd-test
export PT_SCHEMA_DIFF_SNAPSHOT_DIR=/tmp/sd-test/snapshots
B=./bin/pt-schema-diff
PROD=h=127.0.0.1,u=sd_reader,p=Test-pass-123,D=sd_prod
```

`setup.sh` creates the following. `teardown.sh` removes all of it.

| Object | What it is |
|---|---|
| `sd_prod` | The "production" schema ([testing/prod.sql](testing/prod.sql)), the **source**. |
| `sd_staging` | A drifted copy with a few rows ([testing/staging.sql](testing/staging.sql)), the **target**. Each difference is commented with the rule it exercises. |
| `sd_big.page_views` | 1.2M rows, for the large-table advice. |
| `sd_reader` | Read-only user (`SELECT` on `sd_%`). Password `Test-pass-123`. |
| `sd_sandbox` | `ALL` on `pt_schema_diff_%` only, for `--verify`. Same password. |

## 1. Compare two live databases with a read-only login

```bash
$B $PROD D=sd_staging ; echo "exit=$?"
```

The target DSN `D=sd_staging` takes host, user and password from the source DSN.

**Expect:**
- Exit `2`.
- Summary: `6 tables: 1 to create, 4 to change, 1 only in the target … 3 critical, 16 warnings, 6 suggestions, 7 safe`.
- Tables sorted most-severe first, then the migration script.

Check a few findings against the comments in `staging.sql`:
- `customers`: `name` → `full_name` is a **rename** (data kept), not a drop and an add.
- `customers`: `notes` `mediumtext` → `text` is **CRITICAL**, with a `SELECT MAX(LENGTH(notes))` check query.
- `customers`: `status` gaining `'deleted'` at the end is **OK / instant**.
- `customers`: the `legacy_flag` drop is **CRITICAL** and commented out in the script.
- `orders`: `customer_id` → `idx_customer` and `idx_ref_old` → `idx_ref` are **index renames**. The foreign key is added in the last phase.
- `events`: the primary key change rebuilds the table. Column order and partitioning are reported but **not scripted**.

## 2. Test the script on a sandbox before running it

```bash
$B $PROD D=sd_staging --include-drops --verify u=sd_sandbox --format sql --output /tmp/sd-test/migrate.sql ; echo "exit=$?"
$B $PROD D=sd_staging --include-drops --verify u=sd_sandbox --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["verify"])'
```

**Expect:**
- Exit `2` (differences found, script test passed).
- `ok: True`, `count: 8`, and two `expected` entries (column order, partitioning).
- Afterwards `SHOW DATABASES LIKE 'pt\_schema\_diff\_%'` returns nothing: the scratch database was dropped.

## 3. Apply the script for real, then compare again

```bash
less /tmp/sd-test/migrate.sql                       # read it first, as you would in production
mysql -uroot sd_staging < /tmp/sd-test/migrate.sql
$B $PROD D=sd_staging ; echo "exit=$?"
mysql -uroot -e "SELECT id, email, full_name, status FROM sd_staging.customers; SELECT * FROM sd_staging.orders"
```

**Expect:**
- The script runs without errors.
- The new diff shows `0 to change … 5 identical`, plus only the `events` column-order and partitioning notes.
- The customers keep their names under `full_name` (proof the rename kept the data). `orders.total_cents` is filled in (2500, 750).

## 4. Large tables: pt-online-schema-change advice

```bash
mysqldump -uroot --no-data sd_big > /tmp/sd-test/big.sql
sed -i '' 's/`id` int NOT NULL AUTO_INCREMENT/`id` bigint NOT NULL AUTO_INCREMENT/' /tmp/sd-test/big.sql   # Linux: sed -i
$B /tmp/sd-test/big.sql u=root,S=/tmp/mysql.sock,D=sd_big
```

**Expect:**
- A `Large table: ~1,2xx,xxx rows` warning.
- A ready command: `pt-online-schema-change --alter 'MODIFY COLUMN `id` bigint NOT NULL AUTO_INCREMENT' D=sd_big,t=page_views --dry-run`.

If Percona Toolkit is installed, run that command with `h=127.0.0.1,u=root` added to the DSN to confirm the toolkit accepts it.

## 5. Snapshots: track drift over time

```bash
$B --save-snapshot "staging baseline" u=root,S=/tmp/mysql.sock,D=sd_staging
$B --list-snapshots
mysql -uroot -e "ALTER TABLE sd_staging.orders ADD INDEX idx_amount (amount), DROP INDEX idx_ref"
$B snapshot:latest u=root,S=/tmp/mysql.sock,D=sd_staging --format sql
$B --delete-snapshot <ID from the list>
```

**Expect:** a script that puts the server back to the snapshot: `DROP INDEX idx_amount, ADD INDEX idx_ref (ref_code)`.

## 6. File inputs

```bash
mysqldump -uroot --no-data sd_prod > /tmp/sd-test/prod_dump.sql
$B /tmp/sd-test/prod_dump.sql testing/prod.sql ; echo "exit=$?"
```
**Expect:** exit `0`. A dump and the hand-written file describe the same schema.

```bash
mysql -uroot -E sd_prod -e 'SHOW CREATE TABLE orders' > /tmp/sd-test/pasted.txt   # -E = \G output
$B /tmp/sd-test/pasted.txt D=sd_prod,u=root,S=/tmp/mysql.sock --tables orders ; echo "exit=$?"
```
**Expect:** exit `0`. Terminal `\G` output is read correctly.

```bash
cat testing/prod.sql | $B - testing/staging.sql --format sql | head -20
```
**Expect:** standard input works as a side.

## 7. Report formats

```bash
$B testing/prod.sql testing/staging.sql --format html --output /tmp/sd-test/report.html --ddl-diff --title "Prod vs staging"
open /tmp/sd-test/report.html          # check it, then File → Print → Save as PDF
$B testing/prod.sql testing/staging.sql --format markdown > /tmp/sd-test/report.md
$B testing/prod.sql testing/staging.sql --format json | python3 -m json.tool > /dev/null && echo "valid json"
$B testing/prod.sql testing/staging.sql --show-same --ddl-diff | less
```

**Expect:**
- The HTML report has the KPI tiles, the comparison table, colored findings per table, the SQL, the DDL diffs and the script.
- The HTML follows dark mode and prints cleanly.

## 8. Options and filters

| Try | Expect |
|---|---|
| `--tables orders,customers` / `--ignore-tables events` | Only or all-but those tables. |
| `--use-database other` / `--no-use` | The `USE` line changes or disappears. |
| `--script /tmp/sd-test/s.sql` | Report on stdout, script in the file. |
| `--ask-pass` with `h=127.0.0.1,u=sd_reader,D=sd_prod` | Password prompt with no echo. |
| `--defaults-file` with a `[client]` file holding `user`/`password` | Same as the DSN values. |
| Options after the arguments (`$B a.sql b.sql --format sql`) | Work the same as before them. |

## 9. Errors (exit 1, with a clear message)

```bash
$B h=127.0.0.1,u=sd_reader,p=wrong,D=sd_prod D=sd_staging     # Access denied
$B h=127.0.0.1,u=sd_reader D=sd_staging                        # needs a database (D=name)
$B nofile.sql testing/prod.sql                                 # neither a file nor a DSN
$B --format pdf testing/prod.sql testing/staging.sql           # invalid --format
$B $PROD D=sd_staging --verify u=sd_reader                     # sandbox login without CREATE: exits 3
```

## 10. Your own schemas (most valuable)

Point it at a real pair you know well, for example production against staging through read-only accounts:

```bash
$B h=prod-host,u=reader,D=app h=staging-host,D=app --ask-pass --format html --output /tmp/sd-test/real.html
```

Look for:
- **False differences:** things reported as changed that are really the same.
- **Missed differences:** compare with `--ddl-diff`.
- **Wrong risk or algorithm labels:** things you know run instant, online or as a copy on your version.

Then run `--verify` against a sandbox that runs the **same MySQL version as the target**.

## 11. Other MySQL versions

The Percona team will ask about MySQL 8.0, 8.4 and Percona Server. With Docker running:

```bash
docker run -d --name ps84 -e MYSQL_ROOT_PASSWORD=pw -p 3384:3306 percona/percona-server:8.4
docker run -d --name my80 -e MYSQL_ROOT_PASSWORD=pw -p 3380:3306 mysql:8.0
# wait ~30s, then for each port:
MYSQL_OPTS="-uroot -ppw -h127.0.0.1 -P3384" ./testing/setup.sh
make test-live DSN=h=127.0.0.1,P=3384,u=root,p=pw
$B h=127.0.0.1,P=3384,u=root,p=pw,D=sd_prod D=sd_staging --include-drops --verify D=sd_staging
```

## Clean up

```bash
./testing/teardown.sh        # drops sd_prod, sd_staging, sd_big, the two users, /tmp/sd-test
```

## Reporting what you find

For each problem, save the two `CREATE TABLE` statements (`SHOW CREATE TABLE` on both sides) and the output. A pair of files that reproduces it can go straight into `testdata/` as a regression test.
