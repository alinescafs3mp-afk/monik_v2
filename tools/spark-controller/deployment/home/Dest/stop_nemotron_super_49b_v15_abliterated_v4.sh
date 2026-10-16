#!/bin/bash
set -euo pipefail
[ "$#" -eq 0 ] || { echo "This model wrapper accepts no arguments" >&2; exit 2; }
exec /usr/bin/sudo -n -- /usr/local/sbin/spark-modelctl-v4 stop nemotron-super-49b-v15-abliterated
