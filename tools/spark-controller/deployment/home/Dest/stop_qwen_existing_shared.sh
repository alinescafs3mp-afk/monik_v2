#!/bin/sh
set -eu
test "$#" -eq 0
exec sudo -n /usr/local/sbin/spark-modelctl-v4 stop qwen-existing-observation
