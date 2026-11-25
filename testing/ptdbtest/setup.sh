#!/usr/bin/env bash
# Creates the ptdbtest database on two servers with different table structures, and loads data.
#   SOURCE (arundb2, 10.30.51.44):     the wanted structure, 1,000 customers and 1,000 orders
#   TARGET (arunpxcnd1, 10.30.51.106): an older structure, 1,000 customers and 1,200,000 orders
# Credentials come from an option file, so no password is on the command line:
#   CNF (default ~/.ptdbtest.cnf) with
#     [client]
#     user=percona
#     password=...
# Usage: ./setup.sh            create and load
#        ./setup.sh --drop     drop ptdbtest on both servers
set -euo pipefail
cd "$(dirname "$0")"
SOURCE_HOST=${SOURCE_HOST:-10.30.51.44}
TARGET_HOST=${TARGET_HOST:-10.30.51.106}
CNF=${CNF:-$HOME/.ptdbtest.cnf}
ROWS=${ROWS:-1200000}
BATCH=100000
m() { local host=$1; shift; mysql --defaults-extra-file="$CNF" -h "$host" "$@"; }

if [ "${1:-}" = "--drop" ]; then
  for h in "$SOURCE_HOST" "$TARGET_HOST"; do m "$h" -e "DROP DATABASE IF EXISTS ptdbtest"; echo "Dropped ptdbtest on $h"; done
  exit 0
fi

for h in "$SOURCE_HOST" "$TARGET_HOST"; do
  echo "$h: $(m "$h" -N -e "SELECT CONCAT(@@hostname, ' ', @@version)")"
  if [ -n "$(m "$h" -N -e "SHOW DATABASES LIKE 'ptdbtest'")" ]; then
    echo "ptdbtest already exists on $h. Run ./setup.sh --drop first." >&2
    exit 1
  fi
done

load_customers() {
  m "$1" ptdbtest -e "SET SESSION cte_max_recursion_depth = 2000;
    INSERT INTO customers (email, full_name)
    WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000)
    SELECT CONCAT('user', i, '@example.com'), CONCAT('Customer ', i) FROM n;"
}

# Inserts rows [from, to] into orders, using only the columns both structures have.
load_orders() {
  local host=$1 from=$2 to=$3
  m "$host" ptdbtest -e "SET SESSION cte_max_recursion_depth = $((to - from + 10));
    INSERT INTO orders (customer_id, status, amount, note, created_at)
    WITH RECURSIVE n(i) AS (SELECT $from UNION ALL SELECT i + 1 FROM n WHERE i < $to)
    SELECT 1 + i MOD 1000, ELT(1 + i MOD 4, 'new', 'paid', 'shipped', 'cancelled'), ROUND(5 + (i MOD 50000) / 100, 2),
           IF(i MOD 10 = 0, CONCAT('order note ', i), NULL), NOW() - INTERVAL i MINUTE
    FROM n;"
}

echo "Creating ptdbtest on the source ($SOURCE_HOST)..."
m "$SOURCE_HOST" -e "CREATE DATABASE ptdbtest"
m "$SOURCE_HOST" ptdbtest < source.sql
load_customers "$SOURCE_HOST"
load_orders "$SOURCE_HOST" 1 1000

echo "Creating ptdbtest on the target ($TARGET_HOST)..."
m "$TARGET_HOST" -e "CREATE DATABASE ptdbtest"
m "$TARGET_HOST" ptdbtest < target.sql
load_customers "$TARGET_HOST"
# In batches, so each transaction stays small (PXC replicates every transaction to all nodes).
for ((from = 1; from <= ROWS; from += BATCH)); do
  to=$((from + BATCH - 1)); [ "$to" -gt "$ROWS" ] && to=$ROWS
  load_orders "$TARGET_HOST" "$from" "$to"
  echo "  orders: $to / $ROWS rows"
done
m "$TARGET_HOST" ptdbtest -e "ANALYZE TABLE customers, orders" >/dev/null

for h in "$SOURCE_HOST" "$TARGET_HOST"; do
  echo "$h:"
  m "$h" -t -e "SELECT TABLE_NAME, TABLE_ROWS, ROUND((DATA_LENGTH + INDEX_LENGTH) / 1048576, 1) AS size_mb
                FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'ptdbtest' ORDER BY TABLE_NAME"
done
echo "Done. Next: ./run.sh"
