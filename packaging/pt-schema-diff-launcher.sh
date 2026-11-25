#!/bin/sh
# pt-schema-diff launcher for the Linux bundle: runs the binary built for this machine's architecture.
# The bundle holds this script (as pt-schema-diff) next to pt-schema-diff-linux-amd64 and pt-schema-diff-linux-arm64.
# It may be called through a symlink (e.g. /usr/local/bin/pt-schema-diff), so resolve its real location first.
self=$(readlink -f "$0" 2>/dev/null || echo "$0")
dir=$(dirname "$self")
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "pt-schema-diff: no binary for architecture $(uname -m) (supported: x86_64, aarch64)" >&2; exit 1 ;;
esac
exec "$dir/pt-schema-diff-linux-$arch" "$@"
