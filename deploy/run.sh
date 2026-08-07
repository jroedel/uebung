#!/usr/bin/env bash
# Server-side launcher for Übung Club. Uploaded by deploy.sh; not run locally.
#
# One script for both supervision styles, so systemd and the nohup fallback
# cannot drift into launching the app differently. It execs the binary rather
# than backgrounding it, which is what systemd wants and what makes the PID file
# in the nohup path point at the real process.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")"

# Secrets live here, never in the flags: a command line is visible in ps output
# to every other account on a shared machine.
if [ -f ./uebung.env ]; then
	set -a
	# shellcheck disable=SC1091
	. ./uebung.env
	set +a
fi

: "${UEBUNG_PORT:?UEBUNG_PORT is required}"
: "${UEBUNG_LINK_BASE:?UEBUNG_LINK_BASE is required}"
: "${UEBUNG_MAIL_FROM:?UEBUNG_MAIL_FROM is required}"

if [ -z "${UEBUNG_SMTP_PASSWORD:-}" ]; then
	# Not fatal: with no SMTP configured the app logs sign-in links instead of
	# sending them, which is a survivable degraded state and better than
	# refusing to start. It is still wrong in production, so say so loudly.
	echo "run.sh: WARNING no UEBUNG_SMTP_PASSWORD — sign-in links will be written to the log, not emailed" >&2
fi

exec ./uebung \
	-addr "127.0.0.1:${UEBUNG_PORT}" \
	-data "$PWD/uebung.db" \
	-link-base "${UEBUNG_LINK_BASE}" \
	-mail-from "${UEBUNG_MAIL_FROM}" \
	-smtp-host "${UEBUNG_SMTP_HOST:-}" \
	-smtp-port "${UEBUNG_SMTP_PORT:-587}" \
	-smtp-user "${UEBUNG_SMTP_USER:-}" \
	-trust-proxy
