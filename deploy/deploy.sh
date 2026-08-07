#!/usr/bin/env bash
# Deploy Übung Club.
#
# This file is committed; the server's identity is not. Host, account and port
# come from deploy/deploy.env (gitignored) or from the environment, so nothing
# here reveals where the app runs. See deploy.env.example.
#
# FOR A HUMAN TO RUN. Every subcommand opens an SSH session to production,
# including the read-only ones, so agents must not invoke this — see the
# "Production access" section of AGENTS.md, and production_recon.md for what is
# already known about the server without looking.
#
# Usage:
#   deploy/deploy.sh probe        what does this server support? (read-only)
#   deploy/deploy.sh install      one-time: directories, supervisor, cron, htaccess
#   deploy/deploy.sh              build, upload, restart, health-check
#   deploy/deploy.sh --skip-tests skip `make test` (do not make a habit of it)
#   deploy/deploy.sh backup       take a database backup and leave the app running
#   deploy/deploy.sh logs         tail the server log
#   deploy/deploy.sh status       is it up?
#
# The deploy is ordered so the risky part is reversible: the previous binary is
# kept, the database is copied while the app is stopped (so the copy cannot be
# torn), and a failed health check rolls the binary back and restarts before
# exiting non-zero.
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Usage is answerable without configuration, so it comes before the checks below.
case "${1:-}" in
-h | --help | help)
	awk '/^# Usage:/{f=1} f && !/^#/{exit} f{sub(/^# ?/, ""); print}' "${BASH_SOURCE[0]}"
	exit 0
	;;
esac

# --- configuration ---------------------------------------------------------

if [ -f "$SCRIPT_DIR/deploy.env" ]; then
	set -a
	# shellcheck disable=SC1091
	. "$SCRIPT_DIR/deploy.env"
	set +a
fi

if [ -z "${SSH_HOST:-}" ] || [ -z "${SSH_USER:-}" ]; then
	printf '\033[31mxx\033[0m %s\n' \
		"SSH_HOST and SSH_USER are not set." \
		"They are kept out of git on purpose. Create the file:" \
		"    cp deploy/deploy.env.example deploy/deploy.env" \
		"then fill it in (or export the variables)." >&2
	exit 1
fi
SSH_PORT="${SSH_PORT:-22}"

APP_DIR="${APP_DIR:-~/uebung}"
DOCROOT="${DOCROOT:-~/public_html/uebung.club}"
APP_PORT="${APP_PORT:-8402}"
SUPERVISOR="${SUPERVISOR:-systemd}"

PUBLIC_URL="${PUBLIC_URL:-https://uebung.club}"
MAIL_FROM="${MAIL_FROM:-hi@uebung.club}"
SMTP_HOST="${SMTP_HOST:-}"
SMTP_PORT="${SMTP_PORT:-587}"
SMTP_USER="${SMTP_USER:-$MAIL_FROM}"

KEEP_BACKUPS="${KEEP_BACKUPS:-14}"

# --- plumbing --------------------------------------------------------------

# Remote paths are expanded by the remote shell, so APP_DIR may contain ~.
remote() { ssh -p "$SSH_PORT" "$SSH_USER@$SSH_HOST" "$@"; }
# Run a command from inside the application directory. APP_DIR is relative to the
# remote home, so anything that needs to reach the app's files must cd first.
remote_in_app() { remote "cd $APP_DIR && $*"; }
remote_script() { ssh -p "$SSH_PORT" "$SSH_USER@$SSH_HOST" bash -s; }
push() { scp -q -P "$SSH_PORT" "$1" "$SSH_USER@$SSH_HOST:$2"; }

log() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!!\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[31mxx\033[0m %s\n' "$*" >&2
	exit 1
}

require() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }

# report_source says which commit is about to be built, and warns when that is
# probably not what the operator intended. deploy.sh builds from the working tree,
# not from origin/main, so a stale checkout or an uncommitted edit ships silently
# otherwise.
report_source() {
	local branch head dirty behind
	branch=$(git -C "$REPO_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "?")
	head=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo "?")
	log "building from $branch @ $head"

	dirty=$(git -C "$REPO_DIR" status --porcelain 2>/dev/null | grep -cv '^??' || true)
	if [ "${dirty:-0}" -gt 0 ]; then
		warn "$dirty uncommitted change(s) in tracked files will be included in this build"
	fi

	git -C "$REPO_DIR" fetch -q origin 2>/dev/null || true
	behind=$(git -C "$REPO_DIR" rev-list --count HEAD..origin/main 2>/dev/null || echo 0)
	if [ "${behind:-0}" -gt 0 ]; then
		warn "this checkout is $behind commit(s) behind origin/main:"
		git -C "$REPO_DIR" log --oneline HEAD..origin/main 2>/dev/null | sed 's/^/     /' >&2
	fi
}

# assert_layout_sane catches a configuration that would put the database inside
# the web root. It is a string check, so it runs before anything is uploaded.
assert_layout_sane() {
	# Catch a path that bash expanded locally. `APP_DIR=~/x` in deploy.env is an
	# unquoted assignment, and bash tilde-expands those, so the value silently
	# becomes the *developer's* home directory and gets sent to the server as an
	# absolute path that means nothing there. Symptom: mkdir failing on a path
	# under /home/<your-username>.
	local d
	for d in "$APP_DIR" "$DOCROOT"; do
		case "$d" in
		"$HOME"/* | "$HOME")
			die "$d looks like a path on THIS machine (it starts with \$HOME).
   deploy.env probably has APP_DIR=~/... — bash expands the tilde in an unquoted
   assignment. Use a path relative to the remote home instead:
       APP_DIR=public_html/uebung.club
       DOCROOT=public_html/uebung.club/public"
			;;
		esac
	done

	case "$APP_DIR" in
	"$DOCROOT" | "$DOCROOT"/*)
		die "APP_DIR ($APP_DIR) is inside DOCROOT ($DOCROOT). The database would be web-reachable."
		;;
	esac
}

# assert_not_exposed proves from the outside that the document root really is the
# public/ subdirectory.
#
# This is the check that makes the nested layout safe. The app directory holds
# uebung.db, uebung.env and the scripts; if konsoleH's document root still points
# at the app directory rather than public/, Apache serves those files directly and
# a stranger can download every account address and session hash. It cannot be
# verified from the server side, because the docroot is a panel setting, not a
# file — so it is verified the only way that is conclusive: by asking for the
# files over HTTPS and requiring not-200.
#
# With the docroot correct, .htaccess proxies everything, and these paths answer
# 404 (app up) or 503 (app down) — never 200.
assert_not_exposed() {
	local exposed=() f code
	for f in uebung.env uebung.db run.sh supervise.sh; do
		code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$PUBLIC_URL/$f" 2>/dev/null || echo 000)
		if [ "$code" = "200" ]; then
			exposed+=("$f")
		fi
	done

	if [ ${#exposed[@]} -gt 0 ]; then
		warn "these application files are being served over the web:"
		printf '     %s/%s\n' "$PUBLIC_URL" "${exposed[@]}" >&2
		die "the konsoleH document root is not $DOCROOT.
   Set it to /uebung.club/public under Services > Server Configuration >
   Change document root, then re-run. Refusing to continue: uebung.db holds
   every account's address and their session hashes."
	fi
}

# --- subcommands -----------------------------------------------------------

# probe reports what the server can do. Read-only: it changes nothing, so it is
# safe to run before you have decided anything.
cmd_probe() {
	log "probing $SSH_HOST"
	remote_script <<'PROBE'
set -u
printf 'user            : %s (uid %s)\n' "$(id -un)" "$(id -u)"
printf 'home            : %s\n' "$HOME"
printf 'kernel          : %s\n' "$(uname -sr)"

printf 'systemd present : '; [ -d /run/systemd/system ] && echo yes || echo no
printf 'user bus        : '; [ -S "/run/user/$(id -u)/bus" ] && echo yes || echo 'no (systemctl --user will not work)'
printf 'systemctl --user: '
if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  echo yes
else
  echo no
fi
printf 'lingering       : '
if command -v loginctl >/dev/null 2>&1; then
  loginctl show-user "$(id -un)" 2>/dev/null | grep -i '^Linger=' || echo 'unknown (loginctl gave nothing)'
else
  echo 'no loginctl'
fi

printf 'crontab         : '; command -v crontab >/dev/null 2>&1 && echo yes || echo no
printf 'flock           : '; command -v flock >/dev/null 2>&1 && echo yes || echo 'no (supervise.sh needs it)'
printf 'setsid          : '; command -v setsid >/dev/null 2>&1 && echo yes || echo 'no (supervise.sh needs it)'
printf 'sqlite3 cli     : '; command -v sqlite3 >/dev/null 2>&1 && echo yes || echo 'no (backups use cp instead)'
printf 'curl            : '; command -v curl >/dev/null 2>&1 && echo yes || echo no

echo
echo "verdict:"
if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  echo "  SUPERVISOR=systemd will work. Enable lingering once if Linger=no:"
  echo "      loginctl enable-linger $(id -un)"
else
  echo "  systemctl --user is unavailable. Use SUPERVISOR=nohup."
fi
PROBE
}

# cmd_install prepares the server. Idempotent; safe to re-run.
cmd_install() {
	assert_layout_sane

	log "creating $APP_DIR and $DOCROOT"
	# 711 on the application directory, not 700.
	#
	# The document root is *inside* it, and reaching a directory requires execute
	# permission on every parent. Apache does not run as this account, so 700 made
	# it impossible to traverse into public/ and every URL answered 403 — including
	# paths that do not exist, which is the tell: a missing file gives 404, a
	# path Apache cannot walk gives 403.
	#
	# 711 grants traverse without read, so the directory still cannot be listed.
	# Confidentiality of the database does not rest on this in any case: it lives
	# above the document root, so no URL maps to it at all. The mode is
	# defence-in-depth, and 600 on the two sensitive files below is the rest of it.
	remote "mkdir -p $APP_DIR/backups $DOCROOT && chmod 711 $APP_DIR && chmod 700 $APP_DIR/backups && chmod 755 $DOCROOT"

	log "uploading run.sh and supervise.sh"
	push "$SCRIPT_DIR/run.sh" "$APP_DIR/run.sh"
	push "$SCRIPT_DIR/supervise.sh" "$APP_DIR/supervise.sh"
	remote "chmod 700 $APP_DIR/run.sh $APP_DIR/supervise.sh"
	# Belt and braces on the two files that would matter if the layout were ever
	# mangled: the database and the file holding the SMTP password.
	remote "cd $APP_DIR && chmod 600 uebung.env 2>/dev/null; chmod 600 uebung.db 2>/dev/null; true"

	install_env
	install_htaccess

	case "$SUPERVISOR" in
	systemd) install_systemd ;;
	nohup) install_cron ;;
	*) die "SUPERVISOR must be 'systemd' or 'nohup', got '$SUPERVISOR'" ;;
	esac

	log "checking the document root is not serving the application directory"
	assert_not_exposed
	log "install complete — now run: deploy/deploy.sh"
}

# install_env writes the non-secret settings run.sh needs, and creates a
# placeholder for the secret WITHOUT overwriting one that already exists.
install_env() {
	log "writing $APP_DIR/uebung.env (preserving any existing password)"
	remote_script <<EOF
set -euo pipefail
cd $APP_DIR
touch uebung.env; chmod 600 uebung.env

# Keep whatever password is already there; replace everything else.
pw=\$(grep -E '^UEBUNG_SMTP_PASSWORD=' uebung.env 2>/dev/null || true)

cat > uebung.env <<CONF
# Written by deploy.sh. The password line below is preserved across deploys and
# is never uploaded from a developer machine.
UEBUNG_PORT=$APP_PORT
UEBUNG_LINK_BASE=$PUBLIC_URL/auth/callback
UEBUNG_MAIL_FROM=$MAIL_FROM
UEBUNG_SMTP_HOST=$SMTP_HOST
UEBUNG_SMTP_PORT=$SMTP_PORT
UEBUNG_SMTP_USER=$SMTP_USER
CONF

if [ -n "\$pw" ]; then
  printf '%s\n' "\$pw" >> uebung.env
else
  echo '# UEBUNG_SMTP_PASSWORD=  <-- set this by hand, then restart' >> uebung.env
fi
chmod 600 uebung.env
EOF

	if ! remote "grep -qE '^UEBUNG_SMTP_PASSWORD=.+' $APP_DIR/uebung.env"; then
		warn "no UEBUNG_SMTP_PASSWORD on the server yet."
		warn "sign-in emails will be written to the log instead of sent. Fix with:"
		warn "  ssh -p $SSH_PORT $SSH_USER@$SSH_HOST"
		warn "  printf 'UEBUNG_SMTP_PASSWORD=%s\\n' 'the-password' >> $APP_DIR/uebung.env"
	fi
}

install_htaccess() {
	log "installing .htaccess into $DOCROOT (proxy to 127.0.0.1:$APP_PORT)"
	local tmp
	tmp="$(mktemp)"
	sed "s/__APP_PORT__/$APP_PORT/g" "$SCRIPT_DIR/uebung.htaccess" >"$tmp"

	# 644 before pushing, and again after.
	#
	# mktemp creates files mode 600, and scp carries the source mode in the
	# protocol, so the .htaccess landed unreadable by Apache. Apache then answered
	# every request with 403 and the body "Server unable to read htaccess file,
	# denying access to be safe" -- correct behaviour, and a failure mode that looks
	# exactly like a wrong document root unless you read the body.
	#
	# The remote chmod is not redundant: whether scp propagates the mode depends on
	# the implementation, so the mode is asserted where it matters.
	chmod 644 "$tmp"

	remote "mkdir -p $DOCROOT"
	push "$tmp" "$DOCROOT/.htaccess"
	rm -f "$tmp"
	remote "chmod 644 $DOCROOT/.htaccess"

	# A stale challenge directory left by an earlier FileAuth attempt is harmless,
	# but the docroot should otherwise hold nothing: everything else is served by
	# the app, and files here would be reachable if the proxy rule ever broke.
	local strays
	strays="$(remote "ls -A $DOCROOT | grep -v -e '^.htaccess\$' -e '^.well-known\$' || true")"
	if [ -n "$strays" ]; then
		warn "unexpected files in the document root — they are not served by the app,"
		warn "and would become reachable if .htaccess were ever removed:"
		printf '     %s\n' $strays >&2
	fi
}

install_systemd() {
	log "installing user systemd unit"
	remote "mkdir -p ~/.config/systemd/user"
	push "$SCRIPT_DIR/uebung.service" "~/.config/systemd/user/uebung.service"
	remote_script <<'EOF'
set -euo pipefail
systemctl --user daemon-reload
systemctl --user enable uebung.service
if command -v loginctl >/dev/null 2>&1; then
  if ! loginctl show-user "$(id -un)" 2>/dev/null | grep -q '^Linger=yes'; then
    echo "deploy: enabling lingering so the service survives logout and starts at boot"
    loginctl enable-linger "$(id -un)" || \
      echo "deploy: WARNING could not enable lingering; the service will stop when you log out" >&2
  fi
fi
EOF
}

install_cron() {
	log "installing @reboot and watchdog crontab entries"
	remote_script <<EOF
set -euo pipefail
marker='# uebung-club (managed by deploy.sh)'
tmp=\$(mktemp)
crontab -l 2>/dev/null | grep -v "\$marker" > "\$tmp" || true
{
  echo "\$marker"
  echo "@reboot $APP_DIR/supervise.sh start  \$marker"
  echo "*/5 * * * * $APP_DIR/supervise.sh start  \$marker"
} >> "\$tmp"
crontab "\$tmp"
rm -f "\$tmp"
crontab -l | grep uebung-club
EOF
}

cmd_backup() {
	log "backing up the database"
	remote_script <<EOF
set -euo pipefail
cd $APP_DIR
[ -f uebung.db ] || { echo "no database yet, nothing to back up"; exit 0; }
mkdir -p backups
stamp=\$(date -u +%Y%m%dT%H%M%SZ)

# sqlite3 .backup is safe against a live writer; plain cp is not, because it can
# capture a torn page set while WAL is mid-transaction. Prefer the former.
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 uebung.db ".backup 'backups/uebung-\$stamp.db'"
  echo "backups/uebung-\$stamp.db (sqlite3 .backup)"
else
  cp uebung.db "backups/uebung-\$stamp.db"
  [ -f uebung.db-wal ] && cp uebung.db-wal "backups/uebung-\$stamp.db-wal" || true
  echo "backups/uebung-\$stamp.db (cp — no sqlite3 on this host)"
fi

# Keep the most recent few; this is a learner's progress, not a compliance
# archive, and the disk is shared.
ls -1t backups/uebung-*.db 2>/dev/null | tail -n +\$(( $KEEP_BACKUPS + 1 )) | xargs -r rm -f || true
ls -1t backups/ | head -3
EOF
}

# supervisor_cmd emits a command to be run *from inside* $APP_DIR.
#
# It must not embed $APP_DIR. Paths are relative to the remote home, and the remote
# scripts below already `cd $APP_DIR`, so prefixing again produced
# public_html/uebung.club/public_html/uebung.club/supervise.sh and a "No such file
# or directory". Callers use remote_in_app, or are already inside a heredoc that
# has cd'd.
supervisor_cmd() {
	case "$SUPERVISOR" in
	systemd) printf 'systemctl --user %s uebung.service' "$1" ;;
	nohup) printf './supervise.sh %s' "$1" ;;
	*) die "SUPERVISOR must be 'systemd' or 'nohup', got '$SUPERVISOR'" ;;
	esac
}

cmd_status() {
	remote_in_app "$(supervisor_cmd status)" || true
	echo
	log "health endpoint"
	curl -fsS --max-time 15 "$PUBLIC_URL/healthz" && echo || warn "healthz did not answer"
}

cmd_logs() { remote_in_app "tail -n ${1:-80} -f uebung.log"; }

cmd_deploy() {
	local skip_tests="${1:-no}"

	require go
	require ssh
	require scp
	require curl

	report_source

	assert_layout_sane
	log "checking the document root is not serving the application directory"
	assert_not_exposed

	if [ "$skip_tests" = "no" ]; then
		log "make test"
		(cd "$REPO_DIR" && make test)
	else
		warn "skipping tests"
	fi

	log "building static linux/amd64 binary"
	# CGO disabled on purpose: the SQLite driver is pure Go, so the result is a
	# single file with no libc dependency to match against the server's.
	(cd "$REPO_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags='-s -w' -o "$SCRIPT_DIR/.uebung-linux" ./cmd/uebung)

	local size
	size=$(du -h "$SCRIPT_DIR/.uebung-linux" | cut -f1)
	log "uploading binary ($size)"
	push "$SCRIPT_DIR/.uebung-linux" "$APP_DIR/uebung.new"
	rm -f "$SCRIPT_DIR/.uebung-linux"

	# run.sh may have changed too; keep it in step with the binary.
	push "$SCRIPT_DIR/run.sh" "$APP_DIR/run.sh"
	push "$SCRIPT_DIR/supervise.sh" "$APP_DIR/supervise.sh"

	log "stopping, backing up, swapping binary, starting"
	local swapped=yes
	remote_script <<EOF || swapped=no
set -euo pipefail
cd $APP_DIR

# Check the new binary is really here BEFORE stopping anything. Everything below
# is destructive in order -- stop the app, move the old binary aside -- so a
# missing upload must fail while the service is still happily running rather than
# after it is down with no binary to run.
if [ ! -s uebung.new ]; then
  echo "deploy: uebung.new is missing or empty; refusing to touch the running app" >&2
  exit 1
fi
chmod 700 uebung.new run.sh supervise.sh

$(supervisor_cmd stop) || true

# With the writer stopped, a plain copy of the database cannot be torn — which is
# why the backup happens here and not while the app is live.
if [ -f uebung.db ]; then
  mkdir -p backups
  stamp=\$(date -u +%Y%m%dT%H%M%SZ)
  cp uebung.db "backups/uebung-\$stamp.db"
  [ -f uebung.db-wal ] && cp uebung.db-wal "backups/uebung-\$stamp.db-wal" || true
  ls -1t backups/uebung-*.db 2>/dev/null | tail -n +\$(( $KEEP_BACKUPS + 1 )) | xargs -r rm -f || true
  echo "backed up to backups/uebung-\$stamp.db"
fi

# Keep the outgoing binary so a failed health check can be rolled back. A rename
# is used rather than a write-in-place: replacing a running executable's bytes
# gives ETXTBSY, swapping the inode does not.
[ -f uebung ] && mv -f uebung uebung.prev || true
mv -f uebung.new uebung

$(supervisor_cmd start)
EOF

	# Two checks, and keeping them apart is the point.
	#
	# The loopback check proves the new binary runs. The public check proves Apache
	# is proxying to it. Only the first justifies rolling a binary back: a proxy
	# that is not wired up is not the binary's fault, and reverting a good release
	# because Apache is misconfigured discards the release and hides the real
	# problem. That is exactly what happened on the first real deploy — the app was
	# listening happily on loopback while every public URL answered 403.
	local app_ok=no public_ok=no
	if [ "$swapped" = "no" ]; then
		warn "the remote restart failed"
	else
		log "waiting for the app on 127.0.0.1:$APP_PORT"
		for _ in $(seq 1 20); do
			if remote "curl -fsS --max-time 5 http://127.0.0.1:$APP_PORT/healthz" >/dev/null 2>&1; then
				app_ok=yes
				break
			fi
			sleep 1.5
		done
	fi

	if [ "$app_ok" = "yes" ]; then
		log "app is healthy on loopback"
		log "checking it is reachable at $PUBLIC_URL"
		for _ in $(seq 1 10); do
			if curl -fsS --max-time 10 "$PUBLIC_URL/healthz" >/dev/null 2>&1; then
				public_ok=yes
				break
			fi
			sleep 1.5
		done
	fi

	# Record what is running, so "which commit is live?" is answerable later.
	if [ "$app_ok" = "yes" ]; then
		remote_in_app "printf '%s\\n' '$(git -C "$REPO_DIR" rev-parse HEAD 2>/dev/null || echo unknown)' > deployed-commit.txt" || true
	fi

	if [ "$app_ok" = "yes" ] && [ "$public_ok" = "yes" ]; then
		log "healthy: $PUBLIC_URL/healthz"
		remote_in_app "rm -f uebung.prev" || true
		log "deployed"

		return 0
	fi

	if [ "$app_ok" = "yes" ]; then
		# Keep the release. The binary is fine; the web front end is not.
		warn "the app is running and healthy on loopback, but $PUBLIC_URL/healthz does not answer."
		warn "the new binary has been KEPT — this is an Apache/document-root problem, not a bad build."
		warn "check, in this order:"
		warn "  1. konsoleH document root is /uebung.club/public"
		warn "  2. $DOCROOT/.htaccess exists AND is mode 644 — Apache must be able to read it"
		warn "     (curl the site: a body saying \"unable to read htaccess file\" means exactly this)"
		warn "  3. $APP_DIR is mode 711 — Apache must traverse it to reach public/"
		warn "     (403 on every path, including ones that do not exist, means exactly this)"
		exit 1
	fi

	warn "the app did not become healthy on loopback — rolling back"
	remote_script <<EOF
set -euo pipefail
cd $APP_DIR
$(supervisor_cmd stop) || true
if [ -f uebung.prev ]; then
  mv -f uebung uebung.failed
  mv -f uebung.prev uebung
  echo "restored the previous binary (the failed one is kept as uebung.failed)"
else
  echo "no previous binary to restore" >&2
fi
$(supervisor_cmd start) || true
EOF

	remote_in_app "tail -n 40 uebung.log" || true
	die "deploy failed and was rolled back"
}

# --- entry point -----------------------------------------------------------

case "${1:-deploy}" in
probe) cmd_probe ;;
install) cmd_install ;;
backup) cmd_backup ;;
status) cmd_status ;;
logs) cmd_logs "${2:-80}" ;;
deploy) cmd_deploy no ;;
--skip-tests) cmd_deploy yes ;;
*) die "unknown command '$1' (try: probe, install, deploy, backup, status, logs)" ;;
esac
