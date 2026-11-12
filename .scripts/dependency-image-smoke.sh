#!/usr/bin/env bash
set -euo pipefail
: "${EVIDENCE:?Set EVIDENCE to a fresh private output directory}"
exec python3 .scripts/dependency-image-smoke/run.py "${1:?Supply an immutable image ID}"
