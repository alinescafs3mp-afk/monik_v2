#!/bin/sh
set -eu
test "$#" -eq 0 || { test "$#" -eq 1 && test "$1" = --json; }
exec sudo -n /usr/local/sbin/spark-modelctl-v4 status qwen-existing-observation "$@"
