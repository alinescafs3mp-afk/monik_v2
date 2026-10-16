#!/bin/bash
set -euo pipefail
exec /usr/bin/sudo -n -- /usr/local/sbin/spark-modelctl-v4 status gemma4-31b-deckard-heretic "$@"
