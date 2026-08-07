# Production reconnaissance

What is known about the machine Übung Club runs on, so that future work can be
planned without anyone having to go and look.

**Read the "Production access — do not" section of [AGENTS.md](AGENTS.md) first.
Agents must not SSH to this host.** This file exists so you do not need to.

**Deliberately omitted:** the SSH account name, the SSH port, and the connection
string. Those live in `deploy/deploy.env`, which is gitignored, because *this
repository is public*. Everything below is either already publicly discoverable
(DNS, PTR, TLS, mail topology) or harmless on its own.

Last verified 2026-08-07.

---

## The host

| | |
|---|---|
| Provider | Hetzner, managed via the **konsoleH** panel |
| Kind | Dedicated/managed server, **not** shared webhosting |
| Public IPv4 | `78.47.5.22` |
| Public IPv6 | `2a01:4f8:d0a:4267::2` |
| Reverse DNS | `dedi2934.your-server.de`, forward-confirmed, matches the SMTP HELO |
| OS | Linux 6.1 (Debian 12) |
| Account layout | `uebung.club` is an **Addon Domain** under the `schoenstatt.link` konsoleH account |

`tmpcontrol.online` and `patres` live on the same account.

## What the shell can and cannot do

Verified by `deploy/deploy.sh probe`:

| Capability | State |
|---|---|
| `systemd` running | yes |
| **user D-Bus** (`/run/user/<uid>/bus`) | **no** |
| **`systemctl --user`** | **unavailable** — this is the decisive finding |
| `loginctl` | not present, so lingering cannot be enabled |
| `crontab` | yes |
| `flock`, `setsid` | yes |
| `sqlite3` CLI | yes — backups can use `.backup` rather than `cp` |
| `curl` | yes |
| Go toolchain | not present; build locally and upload a static binary |
| Home directory | under `/usr/home/`, not `/home/` |

**Long-lived detached processes survive an SSH disconnect.** Verified by starting
one, disconnecting, reconnecting, and finding it alive. This was the main open
risk about deploying here and it is settled.

Consequence: supervision is `SUPERVISOR=nohup` — `deploy/supervise.sh` plus an
`@reboot` crontab entry and a five-minute watchdog. Do not plan on user systemd.

## Web serving

Apache terminates TLS. The app runs on **loopback only** and is reached through a
`.htaccess` proxy.

- **`ProxyPass` is illegal in `.htaccess`.** Use `RewriteRule … [P]`; `mod_proxy`
  is enabled for it. This is what Hetzner's own konsoleH documentation prescribes.
- `mod_headers` and `mod_rewrite` are available.
- **Non-standard ports are firewalled by default.** konsoleH has an *Open Ports*
  manager (20 rules max) if one is ever genuinely needed — but the app should
  never need one, because it must not be reachable except through Apache's TLS.
- The document root is a **panel setting**, not a file, so it cannot be checked
  over SSH. `deploy.sh` verifies it the only conclusive way: by requesting
  `uebung.db`, `uebung.env`, `run.sh` over HTTPS and requiring not-200.

### Filesystem layout, and the permission that breaks it

`public_html` is a **symlink**. The real path is
`/usr/www/users/<account>/<domain>/` — visible in the app's own log line, where
`-data public_html/uebung.club/uebung.db` resolved to
`/usr/www/users/<account>/uebung.club/uebung.db`.

**Apache does not run as the account user.** This matters for the nested layout:
reaching `<app dir>/public/` requires *execute* permission on `<app dir>`, so
mode **700 on the application directory makes every URL answer 403**. It must be
**711** — traverse without read, so the directory still cannot be listed.

Confidentiality of the database does not depend on that mode. It sits *above* the
document root, so no URL maps to it at all; the mode and `chmod 600` on
`uebung.db`/`uebung.env` are defence-in-depth.

### Reading Apache's status codes here

This distinction cost real time to work out and is worth keeping:

- **403** — the document root exists but is empty (no index, no listing). "Nothing
  deployed."
- **503 on every path** — Apache is proxying to a backend that is down. `[P]` is
  configured and the app is not running.

- **403 on paths that do not exist** — not a missing file (that is 404) but a
  permission problem. Two different causes produce it, and the status code alone
  cannot tell them apart. **Read the response body**, which names the cause:
  - `"You don't have permission to access this resource."` alone — Apache cannot
    traverse or read the document root or one of its parents (see the 711 note
    above).
  - `"Server unable to read htaccess file, denying access to be safe"` — the
    document root is *correct* and `.htaccess` is there, but not readable by
    Apache. It needs mode 644. `mktemp` produces 600 and `scp` carries the source
    mode, which is how this happened here.

`tmpcontrol.online` returns a blanket 503; `uebung.club` returned 403 before
deployment, and 403 again afterwards when the app directory was mode 700.

## TLS

- Free Let's Encrypt certificates via konsoleH → SSL Manager. Current cert covers
  `uebung.club` and `www.uebung.club`.
- **Automated renewal is confirmed** by *Check automated issuance*: DNS-based auth,
  file-based auth and CAA all pass. This works because the zone is on Hetzner
  nameservers with edit access granted.
- Since **1 August 2026** Hetzner only renews free certificates when the process
  is fully automated — Hetzner nameservers with zone access, *or* their hosting
  server. Manual DNS/FileAuth renewals are no longer done.
- No CAA records exist, so any CA may issue. Adding
  `0 issue "letsencrypt.org"` is genuine hardening but would block the Hetzner
  Basic (DigiCert/Thawte) certificate and puts renewal one CA-change from
  breaking. Deliberately left alone.
- konsoleH toggles: HTTPS redirect, OCSP stapling, TLS 1.2+ cipher suite. **HSTS
  is not offered by the panel** — it is set from `.htaccess` via `mod_headers`.

### The ACME trap

konsoleH's FileAuth writes challenges into the document root. A catch-all
`RewriteRule … [P]` hands `/.well-known/acme-challenge/` to the application,
which knows nothing about it, and **renewal breaks silently months later**.

`deploy/uebung.htaccess` excludes `/.well-known/` for exactly this reason.
`tmpcontrol.online` is in the broken state right now.

## DNS

- Authoritative: `ns1.your-server.de`, `ns.second-ns.com`, `ns3.second-ns.de`
  (the konsoleH set — *not* the `hydrogen`/`oxygen`/`helium` set, which belongs to
  the separate `dns.hetzner.com` service).
- Zone record TTL **7200**; registry NS TTL **3600**. Iterating on records has a
  two-hour feedback delay through caching resolvers — **query the authoritative
  nameservers directly** rather than a resolver.
- Registrar is Namecheap; only the delegation lives there now.

## Mail

The app sends sign-in links, so this is load-bearing: no mail means no logins.

| | |
|---|---|
| MX | `dedi2934.your-server.de` (the server itself) |
| Submission | `587` and `465`, both open |
| MTA | Exim 4.96 |
| Extras | ClamAV scanning, Horde webmail |
| Mailboxes | `hi@uebung.club` (app `From:`), `dmarc@uebung.club` (DMARC reports) |

**Outbound mail leaves from `78.47.5.22`** — verified in a recipient's headers,
not assumed. The internal path is
`webmail → sslproxy05.your-server.de (78.46.172.2) → dedi2934 → recipient`, and
the final hop is what SPF evaluates.

⚠️ **`mail.your-server.de` is a different host (`78.46.5.205`)** and is **not**
covered by `a`/`mx`. If submission is ever pointed there, SPF fails on every
sign-in email. Keep `-smtp-host` on the account's own server.

### Authentication, all verified passing at Gmail

```
SPF    v=spf1 a mx ~all
DKIM   selector default2608, 2048-bit RSA
DMARC  v=DMARC1;p=none;sp=none;pct=100;rua=mailto:dmarc@uebung.club;adkim=r;aspf=r
```

Next step when reports look clean: `p=none → quarantine → reject`.

### Deliverability

A fully authenticated test message still landed in Gmail's spam folder. Diagnosis:
the domain is days old with no sending reputation, and the test content was
spam-shaped. Infrastructure is clean — FCrDNS matches, not listed on Barracuda,
SpamCop or SORBS.

Not configured, all modest positives: DNSSEC (no DS at the registry), MTA-STS,
TLS-RPT. MTA-STS needs an HTTPS-served policy file, which this host can now do.

## konsoleH quirks that will waste your time

1. **The panel caches its DNS view.** Status lines lag reality badly — it showed
   Namecheap's SPF and MX values long after the delegation had moved. *Trust
   `dig` against the authoritative nameservers, not the panel.*
2. **The DKIM "public key could not be verified" warning is often stale**, and its
   "normal because of propagation, up to 24 hours" note is misleading: if the
   record was never published there is nothing to propagate. Check with `dig`.
3. **The SPF form does not reflect a record it did not write.** With the *A-records
   may send* / *MX-records may send* boxes unticked, pressing *Set SPF record in
   DNS* publishes `v=spf1 ~all`, authorising **nothing** — every email then fails
   SPF. Tick the boxes first.
4. **The SPF form pre-fills "Include external SPF"** from whatever was there
   before. It carried Namecheap's `spf.efwd.registrar-servers.com` into the new
   record; clear it unless you mean it.
5. **The DMARC form has no `rua` by default**, so `p=none` collects nothing and
   you can never tighten safely. Its `pct=50` is inert under `p=none` but silently
   applies the moment you move to `quarantine`.
6. **`Personal DNS Server` at Namecheap is not how you point at Hetzner.** That
   registers your *own* nameservers. The control is Domain tab → NAMESERVERS →
   Custom DNS.

## Shell gotchas found the hard way

Each of these was a real bug or a real wasted hour:

- **`setsid` forks**, so `$!` is the wrapper's PID, not the app's — observed as
  `$! = 9979` while the surviving process was `9980`. A PID file written from `$!`
  points at a dead process, so `running()` is always false, `stop` never stops the
  app, and a watchdog starts a second instance forever. Have the launched script
  record its own `$$` before `exec`, which preserves the PID.
- **A child inherits the `flock` fd.** `supervise.sh` locks fd 9; the app
  inherited it and held the lock for its whole lifetime, so every later `start`
  blocked forever. Close it in the child (`9>&-`) and use `flock -w`.
- **A child inheriting stdin keeps an `ssh` channel open.** Without `</dev/null`
  the invoking `ssh` waits for EOF and never returns, hanging a deploy right after
  it starts the app.
- **`mktemp` creates files mode 600, and `scp` carries the source mode.** Any file
  pushed from a temporary file arrives unreadable by anyone but the account —
  which for `.htaccess` means Apache refuses every request. `chmod` before *and*
  after the push; whether scp propagates the mode is implementation-dependent.
- **A health check against the public URL cannot tell a bad binary from a
  misconfigured proxy.** The first real deploy rolled back a perfectly good
  release because Apache was answering 403 while the app was listening happily on
  loopback. Check `127.0.0.1:<port>/healthz` on the host first, and only treat a
  failure *there* as grounds to revert.
- **`pkill -f` matches its own command line.** It killed the invoking shell twice
  during this work. Break the pattern (`"sl""eep 400"`) or use a PID.
- **Spamhaus returns `127.255.255.254` from public resolvers** — that is *query
  refused*, not a listing. Their listing codes are `127.0.0.2`–`127.0.0.11`.

## Known problems on neighbouring domains

Not ours to fix, but they will bite someone:

- **`tmpcontrol.online`** — certificate expires **2026-09-26**, its DNS is still at
  Namecheap, and `/.well-known/acme-challenge/` returns 503 because its proxy
  swallows it. FileAuth renewal cannot succeed in that state. Its Go app is down
  and port `33429` is open but refusing connections.
- **`schoenstatt.link`** — publishes **two** DMARC records, so receivers ignore its
  policy entirely (RFC 7489). Also `?all` in its SPF and no DKIM on any common
  selector.
