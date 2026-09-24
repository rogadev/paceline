#!/usr/bin/env bash
# Blocks until every named report file exists and is non-empty, or until the
# time budget runs out, then prints each report. Used by /deep-review because a
# forked skill is not notified when its subagents finish.
#
# Usage: wait-for-reports.sh <scratch-dir> <max-seconds> <name> [<name> ...]
# A name is the report basename without .md (for example: gate output verifier).

dir="$1"; max="$2"; shift 2
waited=0
while :; do
	missing=""
	for n in "$@"; do [ -s "$dir/$n.md" ] || missing="$missing $n"; done
	[ -z "$missing" ] && break
	[ "$waited" -ge "$max" ] && break
	sleep 5; waited=$((waited + 5))
done
echo "waited: ${waited}s | missing:${missing:- none}"
# Print only each report's header (up to its first finding) unless FULL=1:
# the verifier reads the full files, so the orchestrator's context stays small.
for n in "$@"; do
	[ -s "$dir/$n.md" ] || continue
	echo; echo "=================== $n"
	if [ "${FULL:-0}" = 1 ]; then cat "$dir/$n.md"; else awk '/^### /{exit} {print}' "$dir/$n.md" | head -n 8; grep -E '^### \[[BWN]\]' "$dir/$n.md"; fi
done
