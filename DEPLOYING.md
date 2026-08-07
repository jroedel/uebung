# Deploying a Go web app on Hetzner konsoleH

A reusable guide for putting a Go HTTP server into production on the shared
konsoleH host, written from doing it for Übung Club. Copy it into the next project
and work down it.

Two companions:

- **[production_recon.md](production_recon.md)** — facts about the machine itself.
  Read it before planning anything.
- **`deploy/`** — working tooling to copy: `deploy.sh`, `run.sh`, `supervise.sh`,
  `uebung.htaccess`, `uebung.service`, `deploy.env.example`.

**Agents must not SSH to this host** — see *Production access* in
[AGENTS.md](AGENTS.md). Everything below is for a person to run.

**Nothing in this file names the SSH account, port or host.** The repository is
public; those live only in the gitignored `deploy/deploy.env`.

---

## The shape of the thing

```
browser ──HTTPS──▶ Apache (konsoleH cert) ──.htaccess [P]──▶ 127.0.0.1:PORT ──▶ Go app
                                                                                   │
                        <domain>/           app dir, mode 711   ◀── SQLite ────────┘
                        <domain>/public/    document root, 755, .htaccess only
```

The app never speaks TLS, never binds a public port, and never sits inside the
document root. Apache does all three of those jobs.

## What the Go app must do

Get these right in the code and the deployment is mostly mechanical.

| Requirement | Why |
|---|---|
| Bind `127.0.0.1:PORT`, configurable | It must be unreachable except through Apache's TLS. Do **not** open a firewall port. |
| Serve `/healthz`, and have it touch the database | The deploy uses it. "Listening" is not "working". |
| Graceful shutdown on **SIGINT** | Both supervisors send SIGINT so in-flight requests drain and SQLite closes cleanly. |
| `ReadTimeout`, `WriteTimeout`, `IdleTimeout` | `ReadHeaderTimeout` alone lets a connection dawdle once headers are in. |
| Decide cookie `Secure` **once at startup** | Never from `X-Forwarded-Proto`. Apache leaves `r.TLS` nil and `mod_proxy_http` does not send that header, so inference silently ships session cookies without `Secure`. Derive it from your configured public URL scheme. |
| Trust `X-Forwarded-For` only behind an explicit flag | Behind Apache `RemoteAddr` is always `127.0.0.1`, so rate limiting on it puts the whole internet in one bucket. Trusting the header unconditionally lets anyone forge a fresh identity per request. Take the **right-most** entry (one proxy hop). |
| Don't let `http.FileServer` redirect `/index.html` | It answers `301 → ./`, and Apache's `DirectoryIndex` maps `/` onto `index.html` — the two make an infinite loop on your home page. Map the path internally instead. |
| Secrets from the environment, never flags | A command line is visible in `ps` to every other account on the machine. |
| Never log the query string | Tokens travel there; logs outlive tokens. |
| One `*sql.DB` shared by every domain, `MaxOpenConns(1)`, WAL | Two pools on one SQLite file breaks the single-writer assumption. |
| Build with `CGO_ENABLED=0` | Use a pure-Go SQLite driver (`modernc.org/sqlite`). One static file, no libc to match. |

## Per-project setup, in order

The order matters; several steps make the previous one verifiable.

### 1. Domain

Add it in konsoleH as an addon domain.

### 2. DNS — before anything else

1. konsoleH → **DNS Administration → Create zone file**.
2. Verify it answers *before* touching the registrar:
   `dig @ns1.your-server.de <domain> SOA` — `REFUSED` means no zone yet, and
   switching now would black-hole the domain.
3. At the registrar, set nameservers to the konsoleH set (see
   `production_recon.md`). At Namecheap this is **Domain tab → NAMESERVERS →
   Custom DNS** — *not* "Personal DNS Server", which registers your own.
4. Tick **Allow konsoleH to access DNS settings** and press **Save**.

Why first: with Hetzner authoritative *and* zone access granted, certificate
renewal becomes automatic DNSAuth. Since 1 August 2026 Hetzner does not renew free
certificates that need manual authentication.

**Moving DNS drops any registrar-side MX and SPF.** If mail was forwarded there,
recreate it or move to konsoleH mailboxes before switching.

### 3. Document root

konsoleH → **Server Configuration → Change document root** → `/<domain>/public`.

**The directory must exist before it appears in the picker**, so create it first
(`deploy.sh install` does). Until this is set, Apache serves the app directory and
your database is downloadable — `deploy.sh` refuses to run until it can prove
otherwise over HTTPS.

### 4. TLS

SSL Manager → request the Let's Encrypt certificate → **Check automated issuance**
and confirm DNS auth, file auth and CAA are all green. Turn **HTTPS redirect** on.

HSTS is not offered by the panel; set it from `.htaccess`.

### 5. Mail, if the app sends any

If sign-in links or notifications matter, this is load-bearing, not hygiene.

Under **Email → DKIM / SPF / DMARC**:

- **DKIM** — Activate, then press **Set DKIM record in DNS**. Activating alone
  signs mail with a key nobody can verify, which is worse than not signing.
- **SPF** — tick *A-records may send* and *MX-records may send*, set the `all`
  qualifier to **softfail**, and **clear any pre-filled "Include external SPF"**
  left over from a previous provider. With the boxes unticked, pressing Set
  publishes `v=spf1 ~all`, which authorises nothing.
- **DMARC** — add `rua=mailto:…` (the form has none by default, so `p=none`
  collects nothing) and set percentage to 100. Start at `p=none`, then
  `quarantine`, then `reject`.

Then **verify with a real message** to a Gmail/Outlook address and read
`Authentication-Results`. You want `spf=pass`, `dkim=pass`, `dmarc=pass`, and you
want to see which IP SPF passed *for* — that is the only way to confirm which host
your mail actually leaves from.

Create the mailboxes you referenced: the `From:` address and the DMARC `rua`.

A fully authenticated message from a new domain will still often land in spam.
That is reputation, not configuration: mark it not-spam, reply to it, and ramp up
slowly.

### 6. Deploy tooling

Copy `deploy/`, then:

```bash
cp deploy/deploy.env.example deploy/deploy.env   # gitignored
deploy/deploy.sh probe        # read-only: what does the host support?
deploy/deploy.sh install      # dirs, permissions, supervisor, cron, .htaccess
deploy/deploy.sh              # test, build, upload, restart, health-check
```

Put the SMTP password (and any other secret) on the server by hand, once:

```
printf 'APP_SMTP_PASSWORD=%s\n' '…' >> <app dir>/app.env && chmod 600 <app dir>/app.env
```

`install` preserves it across deploys and warns when it is missing.

**Paths in `deploy.env` must be relative to the remote home.** A leading `~` is
tilde-expanded by bash in an unquoted assignment, so it becomes the path of
whoever runs the script, on their own machine.

## Files and permissions

| Path | Mode | Note |
|---|---|---|
| `<domain>/` (app dir) | **711** | **Not 700.** The docroot is inside it, and Apache — which does not run as the account — needs execute to traverse. 700 gives 403 on every URL. 711 grants traverse without listing. |
| `<domain>/public/` | 755 | Apache reads `.htaccess` from here. |
| `<domain>/public/.htaccess` | **644** | `mktemp` creates 600 and `scp` carries the source mode; an unreadable `.htaccess` makes Apache refuse every request. |
| binary, `run.sh`, `supervise.sh` | 700 | |
| `app.env`, the database | 600 | Defence in depth; they are already above the docroot. |
| `backups/` | 700 | |

The database sitting **above** the document root is what actually protects it: no
URL maps there at all. Permissions are the second line, not the first.

## The `.htaccess`, and why the order is the order

```apache
# The app owns every URL; stop Apache resolving index files. Without this,
# DirectoryIndex maps "/" onto index.html and http.FileServer's canonical
# "301 -> ./" loops forever on the home page.
DirectoryIndex disabled

RewriteEngine On

# 1. ACME first. konsoleH FileAuth writes challenges into this docroot, and a
#    catch-all [P] hands them to the app, breaking renewal months later.
RewriteCond %{REQUEST_URI} ^/\.well-known/ [NC]
RewriteRule ^ - [L]

# 2. Force HTTPS. After ACME so HTTP-01 still works; before the proxy so the app
#    never sees cleartext. The second condition prevents a redirect loop if TLS is
#    ever terminated upstream and %{HTTPS} reads off.
RewriteCond %{HTTPS} !=on
RewriteCond %{HTTP:X-Forwarded-Proto} !=https
RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=301,L]

# 3. Everything else to the app. ProxyPass is illegal in .htaccess; [P] is the
#    supported way here.
RewriteRule ^(.*)$ http://127.0.0.1:PORT/$1 [P,QSA,L]

<IfModule mod_headers.c>
  Header always set Strict-Transport-Security "max-age=31536000; includeSubDomains" env=HTTPS
  Header always set X-Content-Type-Options "nosniff"
  Header always set Referrer-Policy "strict-origin-when-cross-origin"
  RequestHeader set X-Forwarded-Proto "https" env=HTTPS
</IfModule>
```

Use `set`, not `setifempty`, on `RequestHeader`: otherwise a client can supply its
own value.

## Keeping it running without root

`systemctl --user` **does not work here** — systemd is present but there is no user
bus and no `loginctl`. Use the `nohup` supervisor with cron:

```
@reboot      <app dir>/supervise.sh start
*/5 * * * *  <app dir>/supervise.sh start     # start is idempotent: also the watchdog
```

Three details that are easy to get wrong and each cost real time:

- **`run.sh` must write its own `$$` before `exec`.** `setsid` forks, so `$!` in
  the supervisor is the wrapper that exits — off by one from the process that
  matters. A PID file pointing at a dead process makes `status` lie, `stop` a
  no-op, and the watchdog start a second instance every five minutes.
- **Close the lock fd in the child** (`9>&-`). Otherwise the app inherits the
  `flock` and holds it for its whole lifetime, so the next `start` — a deploy or a
  watchdog tick — blocks forever. Use `flock -w` as well.
- **Redirect the child's stdin** (`</dev/null`). While it holds an inherited
  stdin, an invoking `ssh` waits for EOF and never returns, hanging the deploy
  right after it starts the app.

## Deploy ordering

```
1. report which commit is being built; warn if dirty or behind origin/main
2. prove the docroot is not serving the app dir (request the db over HTTPS)
3. test, then build static
4. upload binary as <name>.new
5. refuse if <name>.new is missing or empty      <- before anything destructive
6. stop the app
7. copy the database                             <- only now: a live copy can tear
8. mv binary -> .prev, mv .new -> binary         <- rename, not overwrite: ETXTBSY
9. start
10. health-check LOOPBACK, then the public URL
11. roll back only if LOOPBACK failed
```

**Step 11 is the one people get wrong.** A public-URL check cannot tell a bad
binary from a misconfigured web server. Checking only the public URL reverts good
releases whenever Apache is wrong — discarding the release *and* hiding the actual
fault.

## Troubleshooting: symptom to cause

The response **body** matters as much as the status code.

| Symptom | Cause |
|---|---|
| 403 on every path, body says *"Server unable to read htaccess file"* | `.htaccess` not mode 644. The docroot is correct — Apache found the file. |
| 403 on every path, no such message | Apache cannot traverse to the docroot. App dir is 700; make it 711. |
| 403 on a path that **does not exist** | Permissions, not routing. A missing file is 404. |
| 404 on everything, site otherwise fine | Document root points somewhere unexpected. |
| **503** on every path | The `[P]` proxy is configured and the backend is down. |
| `301` with `Location: ./`, loops | `http.FileServer` + `DirectoryIndex`. Fix both sides. |
| Deploy hangs right after starting the app | Child inherited stdin; add `</dev/null`. |
| Second `start` blocks forever | Child inherited the `flock` fd; add `9>&-`. |
| Watchdog starts duplicate instances | PID file holds `setsid`'s pid, not the app's. |
| `mkdir: cannot create directory '/home/<you>'` | `~` in `deploy.env` expanded locally. Use relative paths. |
| konsoleH shows DNS values you know are stale | The panel caches its DNS view. Trust `dig` against the authoritative nameservers. |
| DKIM "public key could not be verified", and it never clears | The record was never published — nothing to propagate. Press *Set DKIM record in DNS*. |

## Verify from outside when you are done

```bash
curl -s -o /dev/null -w '%{http_code}\n' -L https://<domain>/     # 200, 0 redirects
curl -s https://<domain>/healthz                                  # ok
curl -sI http://<domain>/  | grep -i location                     # -> https://
curl -sI https://<domain>/ | grep -i strict-transport             # HSTS present
for f in app.env <db> run.sh; do
  curl -s -o /dev/null -w "$f %{http_code}\n" https://<domain>/$f # 404, never 200
done
```

Then, if the app sends mail, do the one test nothing else substitutes for: request
a real sign-in link and read the headers of the message that arrives.
