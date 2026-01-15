#!/usr/bin/env bash
set -euo pipefail

PAR=20
seq 1 1000 | xargs -n1 -P"$PAR" sh -c "./build/faas-cli funcs execute --name functions/test --params ''" _
