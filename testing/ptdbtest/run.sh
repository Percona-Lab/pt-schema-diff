#!/usr/bin/env bash
# Compares ptdbtest: SOURCE (arundb2) is the structure to match, TARGET (arunpxcnd1) is the one the script changes.
# Reads credentials from CNF (default ~/.ptdbtest.cnf). Extra arguments go to pt-schema-diff (e.g. --format html --output r.html).
set -uo pipefail
cd "$(dirname "$0")"
SOURCE_HOST=${SOURCE_HOST:-10.30.51.44}
TARGET_HOST=${TARGET_HOST:-10.30.51.106}
CNF=${CNF:-$HOME/.ptdbtest.cnf}
if [ -z "${TOOL:-}" ]; then
  # The repository build, or the binary next to this folder (e.g. /home/percona/pt-schema-diff), or the PATH.
  for t in ../../bin/pt-schema-diff ../pt-schema-diff; do [ -x "$t" ] && TOOL=$t && break; done
  TOOL=${TOOL:-pt-schema-diff}
fi
"$TOOL" --defaults-file "$CNF" "h=$SOURCE_HOST,D=ptdbtest" "h=$TARGET_HOST,D=ptdbtest" "$@"
code=$?
echo "exit=$code (0 same, 1 error, 2 differences, 3 verify failed)"
exit $code
