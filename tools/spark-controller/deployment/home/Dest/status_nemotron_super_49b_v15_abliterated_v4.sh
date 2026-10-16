#!/bin/bash
set -euo pipefail
exec /usr/bin/sudo -n -- /usr/local/sbin/spark-modelctl-v4 status nemotron-super-49b-v15-abliterated "$@"
