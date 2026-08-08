#!/usr/bin/env bash
#
# Rehearse a rollback, locally, before shipping a schema migration.
#
#   deploy/rehearse-rollback.sh backups/uebung-20260808T101500Z.db [previous-git-ref]
#
# ---------------------------------------------------------------------------
# THIS SCRIPT IS LOCAL-ONLY. It never connects to any host, opens no SSH session,
# and reads nothing from deploy.env. It is safe for anyone -- including an agent --
# to run. It is not a subcommand of deploy.sh and shares no code with it.
# ---------------------------------------------------------------------------
#
# What it answers
# ---------------
# deploy.sh rolls back by restoring the previous *binary*. It does not restore the
# database, and a migration cannot be undone by swapping an executable. So: what
# does the old build actually do against a database the new build has migrated?
#
# For the deck migration the answer is the worst of the available answers. The old
# build's CREATE TABLE IF NOT EXISTS is a no-op against the rebuilt table, so it
# starts cleanly. /healthz is wired to db.PingContext, which proves the connection
# is alive and reads no table, so the probe answers 200. Every study request then
# fails on a column that no longer exists.
#
# deploy.sh's health check therefore passes, and reports a healthy rollback, while
# the app is unusable. That is what this script demonstrates end to end, with the
# two real binaries, so the failure is something you have seen rather than
# something you have been told about.
#
# Run it whenever a change touches the schema. If the output ever stops matching
# what is described above, the restore procedure in DEPLOYING.md needs revisiting.
#
# The database you name is copied first and never written to.

set -euo pipefail

BACKUP="${1:-}"
PREV_REF="${2:-HEAD~1}"
PORT="${PORT:-8123}"

die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[36m==>\033[0m %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }

[ -n "$BACKUP" ] || die "usage: $0 <pre-migration-database> [previous-git-ref]"
[ -f "$BACKUP" ] || die "no such database: $BACKUP"

command -v git >/dev/null || die "git is required"
command -v go >/dev/null || die "go is required"
command -v curl >/dev/null || die "curl is required"

REPO_ROOT=$(git rev-parse --show-toplevel)
WORK=$(mktemp -d)
OLD_TREE="$WORK/old-tree"
DB="$WORK/rehearsal.db"

# Everything lives in $WORK, and the git worktree has to be removed through git or
# it stays registered in .git/worktrees.
cleanup() {
	[ -n "${NEW_PID:-}" ] && kill "$NEW_PID" 2>/dev/null || true
	[ -n "${OLD_PID:-}" ] && kill "$OLD_PID" 2>/dev/null || true
	wait 2>/dev/null || true
	[ -d "$OLD_TREE" ] && git -C "$REPO_ROOT" worktree remove --force "$OLD_TREE" 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT

cp "$BACKUP" "$DB"
[ -f "$BACKUP-wal" ] && cp "$BACKUP-wal" "$DB-wal"
log "copied $BACKUP -> $DB (the original is never written to)"

# --- build both binaries ---------------------------------------------------

log "building the current binary from the working tree"
( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$WORK/uebung.new" ./cmd/uebung )

log "building the previous binary from $PREV_REF"
git -C "$REPO_ROOT" worktree add --detach "$OLD_TREE" "$PREV_REF" >/dev/null 2>&1 \
	|| die "could not check out $PREV_REF (is it a valid ref?)"
( cd "$OLD_TREE" && CGO_ENABLED=0 go build -o "$WORK/uebung.prev" ./cmd/uebung )
note "previous: $(git -C "$REPO_ROOT" rev-parse --short "$PREV_REF") $(git -C "$REPO_ROOT" log -1 --format=%s "$PREV_REF")"

# start_app <binary> <pidvar>; waits for the port to answer or gives up.
start_app() {
	local bin="$1"
	"$bin" -addr "127.0.0.1:$PORT" -data "$DB" -single-user -insecure-cookies \
		-link-base "http://127.0.0.1:$PORT/auth/callback" >"$WORK/$(basename "$bin").log" 2>&1 &
	local pid=$!

	local i
	for i in $(seq 1 40); do
		if curl -fsS --max-time 2 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
			echo "$pid"
			return 0
		fi
		kill -0 "$pid" 2>/dev/null || break
		sleep 0.25
	done

	echo "$pid"

	return 1
}

status_of() {
	curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$PORT/$1" 2>/dev/null || echo 000
}

# --- 1. the new binary migrates -------------------------------------------

log "starting the NEW binary (this performs the migration)"
NEW_PID=$(start_app "$WORK/uebung.new") || die "the new binary never became healthy; see $WORK/uebung.new.log"
note "healthz  $(status_of healthz)"
note "summary  $(status_of 'api/summary?lang=de')"
note "$(curl -s --max-time 5 "http://127.0.0.1:$PORT/api/summary?lang=de")"

kill "$NEW_PID" 2>/dev/null || true
wait "$NEW_PID" 2>/dev/null || true
NEW_PID=""
log "stopped the new binary; the database is now migrated"

# --- 2. roll back to the old binary ---------------------------------------

log "starting the PREVIOUS binary against the migrated database (this is the rollback)"
OLD_PID=$(start_app "$WORK/uebung.prev") || true

HEALTH=$(status_of healthz)
SUMMARY=$(status_of 'api/summary?lang=de')

note "healthz  $HEALTH"
note "summary  $SUMMARY"

kill "$OLD_PID" 2>/dev/null || true
wait "$OLD_PID" 2>/dev/null || true
OLD_PID=""

# --- 3. the verdict --------------------------------------------------------

echo
if [ "$HEALTH" = "200" ] && [ "$SUMMARY" != "200" ]; then
	printf '\033[31m%s\033[0m\n' "SILENT ROLLBACK FAILURE CONFIRMED"
	echo
	echo "  The rolled-back binary answers /healthz with 200, so deploy.sh would"
	echo "  call the rollback successful -- while /api/summary answers $SUMMARY and"
	echo "  the app is unusable."
	echo
	echo "  A failed deploy of this change therefore needs the DATABASE restored"
	echo "  too, not just the binary. See 'Restoring after a failed migration' in"
	echo "  DEPLOYING.md."
	echo
	echo "  Log tail from the rolled-back binary:"
	sed 's/^/    /' "$WORK/uebung.prev.log" | tail -n 6
elif [ "$HEALTH" = "200" ] && [ "$SUMMARY" = "200" ]; then
	printf '\033[32m%s\033[0m\n' "ROLLBACK IS SAFE"
	echo "  The previous binary serves correctly against the migrated database."
	echo "  This migration is backward-compatible; no database restore is needed."
else
	printf '\033[33m%s\033[0m\n' "ROLLBACK FAILS LOUDLY (healthz=$HEALTH)"
	echo "  The previous binary does not pass its own health check, so deploy.sh"
	echo "  would report the rollback as failed rather than silently succeeding."
	echo "  Still restore the database, but the failure at least announces itself."
fi
echo
