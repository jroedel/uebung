#!/usr/bin/env bash
# Minimal process supervisor for hosts without `systemctl --user`.
#
# Uploaded by deploy.sh and driven by cron:
#
#     @reboot      ~/uebung/supervise.sh start
#     */5 * * * *  ~/uebung/supervise.sh start
#
# `start` is idempotent — it does nothing if the app is already up — so the same
# command serves as boot launcher and as watchdog. flock makes that safe when the
# five-minute watchdog fires while a deploy is mid-restart.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")"

PIDFILE="./uebung.pid"
LOGFILE="./uebung.log"
LOCKFILE="./supervise.lock"

running() {
	[ -f "$PIDFILE" ] || return 1
	local pid
	pid=$(cat "$PIDFILE" 2>/dev/null) || return 1
	[ -n "$pid" ] || return 1
	# Check it is our process and not a recycled PID belonging to something else.
	kill -0 "$pid" 2>/dev/null || return 1
	grep -q uebung "/proc/$pid/cmdline" 2>/dev/null || return 1

	return 0
}

start() {
	if running; then
		return 0
	fi

	# setsid detaches from the cron/ssh session, so the app is not killed when the
	# invoking shell exits — which is the whole failure mode of a bare `&`.
	#
	# </dev/null matters as much as the redirects on stdout/stderr, and is easy to
	# forget: while the child still holds the inherited stdin, an invoking ssh
	# keeps its channel open waiting for EOF and never returns. That hangs
	# `deploy.sh` right after it starts the app — which is exactly how this was
	# found.
	# The pid file is written by run.sh, not from $! here: setsid forks, so $! is
	# the wrapper that exits rather than the app.
	rm -f "$PIDFILE"
	# 9>&- closes the lock fd in the child. Without it the app inherits fd 9 and so
	# holds the flock for its entire lifetime, and the next `start` — a deploy, or
	# the five-minute watchdog — blocks forever on a lock the running app will
	# never release.
	setsid nohup ./run.sh </dev/null >>"$LOGFILE" 2>&1 9>&- &

	# Wait for run.sh to exec the binary and claim the pid file.
	for _ in $(seq 1 25); do
		if running; then
			return 0
		fi
		sleep 0.2
	done

	echo "supervise: the app did not come up; last log lines:" >&2
	tail -n 20 "$LOGFILE" >&2 2>/dev/null || true

	return 1
}

stop() {
	if ! running; then
		rm -f "$PIDFILE"

		return 0
	fi

	local pid
	pid=$(cat "$PIDFILE")
	# SIGINT, not SIGTERM: the app's shutdown handler listens for interrupt and
	# drains in-flight requests before closing the database.
	kill -INT "$pid" 2>/dev/null || true

	for _ in $(seq 1 50); do
		running || break
		sleep 0.2
	done

	if running; then
		echo "supervise: graceful stop timed out, killing $pid" >&2
		kill -KILL "$pid" 2>/dev/null || true
		sleep 1
	fi

	rm -f "$PIDFILE"
}

case "${1:-}" in
start | stop | restart | status) ;;
*)
	echo "usage: $0 {start|stop|restart|status}" >&2
	exit 2
	;;
esac

# status needs no lock and must stay cheap: the deploy health check calls it.
if [ "$1" = "status" ]; then
	if running; then
		echo "running (pid $(cat "$PIDFILE"))"
		exit 0
	fi
	echo "not running"
	exit 1
fi

exec 9>"$LOCKFILE"

# Bounded wait, so a lock held by something unexpected degrades into a clear
# failure rather than a hung deploy or a cron job that never returns.
if ! flock -w 30 9; then
	echo "supervise: could not acquire $LOCKFILE within 30s; another operation is in progress" >&2
	exit 1
fi

case "$1" in
start) start ;;
stop) stop ;;
restart)
	stop
	start
	;;
esac
