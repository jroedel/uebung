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
	setsid nohup ./run.sh >>"$LOGFILE" 2>&1 &
	echo $! >"$PIDFILE"
	sleep 1

	running
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
flock 9

case "$1" in
start) start ;;
stop) stop ;;
restart)
	stop
	start
	;;
esac
