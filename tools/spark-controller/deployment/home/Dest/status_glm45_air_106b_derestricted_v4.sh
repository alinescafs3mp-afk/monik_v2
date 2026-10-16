#!/bin/bash
set -euo pipefail
exec /usr/bin/sudo -n -- /usr/local/sbin/spark-modelctl-v4 status glm45-air-106b-derestricted "$@"
