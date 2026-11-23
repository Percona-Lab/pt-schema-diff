#!/usr/bin/env bash
# Creates the test databases and users for TESTING.md on a MySQL server you can throw data at.
#   sd_prod      the "production" schema (source)
#   sd_staging   a drifted copy with a few rows (target)
#   sd_big       optional: a 1.2M-row table, for the large-table advice (./setup.sh --big)
#   sd_reader    read-only login (SELECT on sd_%)
#   sd_sandbox   login for --verify (ALL on pt_schema_diff_% only)
# Connection: MYSQL_OPTS, default "-uroot" (e.g. MYSQL_OPTS="-uroot -p -h127.0.0.1 -P3306" ./setup.sh).
set -euo pipefail
cd "$(dirname "$0")"
MYSQL_OPTS=${MYSQL_OPTS:--uroot}
m() { mysql $MYSQL_OPTS "$@"; }

echo "Creating sd_prod and sd_staging..."
m -e "DROP DATABASE IF EXISTS sd_prod; DROP DATABASE IF EXISTS sd_staging; CREATE DATABASE sd_prod; CREATE DATABASE sd_staging;"
m sd_prod < prod.sql
m sd_staging < staging.sql

echo "Creating users sd_reader and sd_sandbox (password: Test-pass-123)..."
m -e "DROP USER IF EXISTS 'sd_reader'@'%', 'sd_sandbox'@'%';
      CREATE USER 'sd_reader'@'%' IDENTIFIED BY 'Test-pass-123';
      GRANT SELECT ON \`sd\\_%\`.* TO 'sd_reader'@'%';
      CREATE USER 'sd_sandbox'@'%' IDENTIFIED BY 'Test-pass-123';
      GRANT ALL ON \`pt\\_schema\\_diff\\_%\`.* TO 'sd_sandbox'@'%';"

if [ "${1:-}" = "--big" ]; then
  echo "Creating sd_big.page_views with 1.2M rows (takes a minute)..."
  m -e "DROP DATABASE IF EXISTS sd_big; CREATE DATABASE sd_big;"
  m sd_big -e "CREATE TABLE page_views (id int NOT NULL AUTO_INCREMENT PRIMARY KEY, url varchar(200) NOT NULL, seen datetime NOT NULL) ENGINE=InnoDB;
    SET SESSION cte_max_recursion_depth = 2000000;
    INSERT INTO page_views (url, seen)
      WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1200000)
      SELECT CONCAT('/page/', i MOD 1000), NOW() - INTERVAL i SECOND FROM n;
    ANALYZE TABLE page_views;"
fi
echo "Done. Next: follow TESTING.md."
