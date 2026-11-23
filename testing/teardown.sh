#!/usr/bin/env bash
# Removes everything setup.sh and the tests created.
set -euo pipefail
MYSQL_OPTS=${MYSQL_OPTS:--uroot}
mysql $MYSQL_OPTS -e "DROP DATABASE IF EXISTS sd_prod; DROP DATABASE IF EXISTS sd_staging; DROP DATABASE IF EXISTS sd_big;
  DROP USER IF EXISTS 'sd_reader'@'%', 'sd_sandbox'@'%';"
# Scratch databases --verify could leave behind if it was killed.
for db in $(mysql $MYSQL_OPTS -N -e "SHOW DATABASES LIKE 'pt\\_schema\\_diff\\_%'"); do
  mysql $MYSQL_OPTS -e "DROP DATABASE \`$db\`"
done
rm -rf /tmp/sd-test
echo "Removed the test databases, users and /tmp/sd-test."
