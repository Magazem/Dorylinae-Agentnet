# Early private relay on Hetzner: deploy runbook

Ticket **4.1p** ([49-phase4-tickets.md](../review/49-phase4-tickets.md#41p-early-private-relay-review-of-the-config)),
owner decision **D40**. This puts the already-built relay binary on one Hetzner Cloud VM at
`wss://relay.dorylinae.net`, **for the owner's own team only**. Files:
[`deploy/early/`](../../deploy/early/) (`Caddyfile`, `agentnet-relay.service`, `setup.sh`).

What this is **not**: accounts stay off (no `--accounts`, no OAuth, no invites); there are
**no backups** (below); it is not the 4.1b runbook (rollback, OAuth secret rotation, team
suspension, alerts do not apply yet).

Topology (OD-P4-3 (a), D37):

```
daemons --wss:443--> Caddy (TLS, Let's Encrypt) --http--> relay 127.0.0.1:8787
                                                          metrics 127.0.0.1:9787 (never public)
```

## No backups yet

**No backup of the relay database is configured.** If the VM or its disk is lost, every
envelope queued for an offline peer is lost with it. Mail is delayed, not lost: the sender's
outbox resends until the recipient acknowledges it (relay-hosted.md §3, Restore). Queued
non-mail envelopes are lost. This is acceptable for the owner's own team. **Before any beta
tester's traffic reaches this host, 4.1b's encrypted off-site backup must be running.** That
needs the operator key (OD-P4-11) first.

## 1. Create the VM (Hetzner Cloud Console)

1. Project → **Add Server**. Location: an EU one (Falkenstein, Nuremberg or Helsinki).
2. Image: **Debian 13** if offered, else **Debian 12**. `setup.sh` expects Debian.
3. Type: the smallest shared type. Review 52 needs at least 512 MB of RAM, and every current
   Hetzner type has more. x86 (`CX…`) is simplest. Arm (`CAX…`) also works if you build the
   relay for `arm64` in step 4.
4. Networking: public IPv4 and IPv6 both on.
5. SSH key: add your public key (e.g. `~/.ssh/id_ed25519.pub`). **Do not** use a root password.
6. Backups: leave **off** (your call, see [Owner decisions](#owner-decisions-in-this-runbook)).
7. Create. Note the IPv4 and IPv6 addresses.

## 2. DNS (at the registrar of dorylinae.net)

Create two records, TTL 300 while you set up:

| Name | Type | Value |
|---|---|---|
| `relay.dorylinae.net` | `A` | the VM's IPv4 address |
| `relay.dorylinae.net` | `AAAA` | the VM's IPv6 address (Hetzner shows a `/64`: use `<prefix>::1`) |

Check from your PC: `nslookup relay.dorylinae.net` shows both. Caddy cannot get a
certificate until this resolves, so do it before step 5.

## 3. First login

```
ssh root@<IPv4>
```

Accept the host key only after comparing it with the fingerprint in the Hetzner console, if
it shows one.

## 4. Get the relay binary onto the VM

Use a **signed release** when one exists: download `agentnet_<version>_linux_amd64.tar.gz`
(or `_arm64`) and `SHA256SUMS`. Verify the checksum and signature as
[install.md](../cli/install.md#manual-install-any-os) describes. The archive contains `relay`.

Until then, build it from a clean checkout of `main` on your PC (PowerShell). A plain build
has no `testhooks`:

```
$env:CGO_ENABLED='0'; $env:GOOS='linux'; $env:GOARCH='amd64'   # 'arm64' for a CAX server
go build -trimpath -ldflags "-s -w" -o relay ./cmd/relay
Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH
```

Upload the binary and the config templates:

```
scp relay root@<IPv4>:/root/relay
scp -r deploy/early root@<IPv4>:/root/early
```

On the VM:

```
install -d -m 0755 /opt/agentnet-relay
install -m 0755 /root/relay /opt/agentnet-relay/relay
/opt/agentnet-relay/relay --version
```

## 5. Run setup.sh (as root, on the VM)

If your ISP gives you a **static** IP, restrict SSH to it:

```
SSH_ALLOW_FROM=<your.static.ip> bash /root/early/setup.sh
```

Otherwise leave SSH open to all addresses. It still accepts keys only, and fail2ban bans
repeated failures:

```
bash /root/early/setup.sh
```

Warning: with a wrong `SSH_ALLOW_FROM` you lock yourself out. Keep this SSH session open,
then test a **second** login before closing it. If you are locked out, the Hetzner console's
web terminal still works (Server → Console), and `ufw allow 22/tcp` there undoes it.

`setup.sh` does the following, and is safe to re-run:

- installs `ufw`, `fail2ban`, `unattended-upgrades` and `caddy`, all from Debian's own
  archive. It downloads nothing else;
- SSH: passwords off, root by key only. It refuses to run if `/root/.ssh/authorized_keys`
  holds no key;
- fail2ban's `sshd` jail, reading the journal;
- firewall: inbound 22/tcp (only from `SSH_ALLOW_FROM` if set) and 443/tcp, nothing else;
- automatic security updates, with a reboot at 04:00 (UTC unless you changed the VM's
  timezone) when a kernel or libc update needs one;
- the journal keeps logs for 14 days at most;
- a system user `agentnet-relay` (no login shell, no home). The relay runs as it, with its
  database in `/var/lib/agentnet-relay/` (mode 0700);
- installs `/etc/caddy/Caddyfile` (the Debian default is kept as `Caddyfile.debian-default`)
  and `/etc/systemd/system/agentnet-relay.service`, validates both, and starts Caddy and the
  relay.

Doing it by hand instead: the script is short, and every step can be run line by line.

### The relay's flags (in `agentnet-relay.service`)

| Flag / setting | Value | Why |
|---|---|---|
| `--listen` | `127.0.0.1:8787` | Loopback only; Caddy is the only public listener |
| `--behind-proxy`, `--public-origin` | `wss://relay.dorylinae.net` | TLS ends in Caddy. The relay is **public**: auth v2 is required and pairing v1 is off |
| `--client-ip-header`, `--trusted-proxy` | `X-Forwarded-For`, `127.0.0.1` | Per-prefix limits see the real client. Caddy overwrites any client-sent `X-Forwarded-For` |
| `--db` | `/var/lib/agentnet-relay/relay.db` | systemd `StateDirectory`, owned by the service user |
| `--metrics-listen` | `127.0.0.1:9787` | Operator metrics on loopback only (4.1a) |
| `--max-conns` | `2000` | Review 52 M2 (the default of 5000 is sized for a larger host) |
| `--max-inflight` | `48MiB` | Review 52 M2. The same budget applies again to frames being read, so about 96 MiB at most |
| `GOMEMLIMIT` | `400MiB` | Review 52 M2: about 80 % of a 512 MB VM |
| `--queue-max-total` | `1GiB` | See below |
| `--queue-min-free-disk` | `512MiB` | See below |
| `LimitNOFILE` | `8192` | 2000 + 256 connections exceed the usual 1024 |
| `--accounts` | not passed (off) | D40: the owner's own team only |

**Disk numbers.** Review 52 asked for disk limits scaled to a 2–3 GB volume:

- `--queue-max-total 1GiB`: the SQLite file can grow to about the queue size plus the WAL
  and free pages (the file does not shrink on its own). 1 GiB of queue plus that overhead
  plus the 512 MiB floor still fits a 3 GB volume.
- It is also ample for one team. Each recipient already holds at most 32 MiB, so 1 GiB
  covers about 30 devices that are all offline with full queues.
- `--queue-min-free-disk 512MiB`: below this, new envelopes are refused, but acks and
  deletes (which free space) still work. The floor leaves room for the WAL checkpoint, apt
  and the journal.

The VM's root disk is much larger than 3 GB, so these limits are conservative. The database
stays on the root disk: a separate Hetzner Volume is not needed.

## 6. Watch it start (on the VM)

```
systemctl status agentnet-relay caddy
journalctl -u agentnet-relay -n 20 --no-pager
```

The relay's log must show `public: yes; origins: wss://relay.dorylinae.net; accounts: off`.

```
journalctl -u caddy -n 50 --no-pager | grep -i -E 'certificate|error'
```

Caddy should report that it obtained a certificate for `relay.dorylinae.net`. If it
reports an error, check DNS (step 2) and that 443/tcp is open (`ufw status`).

## 7. Checks

### On the VM

```
ss -tlnp
```

Expected:

- the relay on `127.0.0.1:8787` and `127.0.0.1:9787` only;
- Caddy on `*:443`. It may also show `*:80`, which the firewall blocks, and its admin API on
  `127.0.0.1:2019`;
- `sshd` on 22;
- nothing else listening on a public address.

```
curl -s http://127.0.0.1:8787/healthz        # {"ok":true,"version":"..."}
curl -s http://127.0.0.1:9787/metrics        # relay_connections ...
ufw status verbose                           # 22/tcp and 443/tcp only
```

### From your PC (outside the VM)

In PowerShell type `curl.exe`, not `curl` (an alias for `Invoke-WebRequest` there).

```
curl -sS https://relay.dorylinae.net/healthz              # {"ok":true,...}, valid certificate
curl -m5 -s -o NUL -w "%{http_code}\n" https://relay.dorylinae.net/metrics   # 404 (Unix: -o /dev/null)
curl -m5 http://relay.dorylinae.net:9787/metrics          # must fail (timeout)
curl -m5 http://relay.dorylinae.net:8787/healthz          # must fail (timeout)
curl -m5 http://relay.dorylinae.net:2019/config/          # must fail (Caddy admin API)
curl -m5 http://relay.dorylinae.net/                      # must fail (port 80 closed)
```

Any of the "must fail" lines that answers means something is exposed. **Stop the relay**
(`systemctl stop agentnet-relay`) and fix it before going on.

### A real daemon through it

On each of two of your own machines (or with a teammate):

```
agentnetd install --relay wss://relay.dorylinae.net
agentnet doctor
```

`doctor` must show `relay ok` ("connected, auth v2") and `clock ok`. Then pair the two
machines, or use an existing pairing or team:

```
agentnet pair --new          # machine A: prints a code
agentnet pair <code>         # machine B
agentnet ping @<peer>        # from A: must answer
agentnet request @<peer> question --title "relay check" --brief "reply ok"
```

The request must arrive on B (`agentnet inbox` / `agentnet request list`). To check the
offline queue, stop B's daemon (`agentnet stop`), send a second request from A, then start B
again (`agentnetd install --relay wss://relay.dorylinae.net` restarts it): the request is
delivered.

Record the date and the `relay --version` in HANDOFF.

## Updating the relay binary

```
scp relay root@<IPv4>:/root/relay                       # from your PC
install -m 0755 /root/relay /opt/agentnet-relay/relay   # on the VM
systemctl restart agentnet-relay
journalctl -u agentnet-relay -n 5 --no-pager
```

Relay migrations are forward-only. With no backup, a bad release means a fresh database, so
the queue is lost. Before a release that adds a relay migration, take a manual copy:

```
runuser -u agentnet-relay -- /opt/agentnet-relay/relay backup --db /var/lib/agentnet-relay/relay.db --out /var/lib/agentnet-relay/pre-upgrade.db
```

Delete the copy once the new release runs.

## What changes later (the beta)

This host is **upgraded in place, not thrown away**. When the beta starts:

- add `--accounts github` and 4.2b's OAuth flags (the secret from a root-only file or env
  file, never the repo). The production OAuth app's callback is already
  `https://relay.dorylinae.net/` (D39);
- add `--security-journal` with an off-host sink;
- add the login paths to the Caddyfile's `@relay` matcher (4.2b names them; today only
  `/v1/connect` and `/healthz` are forwarded);
- add 4.1b's daily encrypted backup to another provider, an uptime monitor on `/healthz`
  and alerts (relay-hosted.md §3 and §5), and 4.1b's full runbook
  (`Docs/ops/relay-runbook.md`);
- R-4.2 and the 4.1b config review must pass first.

## Owner decisions in this runbook

The defaults below are chosen; change any of them if you disagree.

- **SSH from anywhere vs your IP only.** The default is anywhere, with keys only and
  fail2ban. Restrict it (`SSH_ALLOW_FROM`) only with a static IP.
- **Root login by key.** No separate admin user is created. Root can log in with a key and
  never with a password. A sudo user plus `PermitRootLogin no` is stricter and adds steps.
- **Automatic reboots at 04:00.** Kernel fixes then apply without you, at the cost of about
  a minute of downtime; daemons reconnect by themselves. Delete
  `/etc/apt/apt.conf.d/52agentnet-relay-reboot` to reboot by hand instead.
- **Caddy from Debian's archive.** It is older than upstream Caddy but covered by Debian
  security updates and needs no third-party apt repository. Upstream's repository is the
  alternative. That is a new signing key to trust, so it is your call.
- **Hetzner "Backups" (VM snapshots).** Left off. The spec wants encrypted backups at
  another provider (relay-hosted.md §3). Today the database holds only sealed ciphertext
  and routing keys, so turning it on is low-risk, but it is not the backup 4.1b requires.
- **A Hetzner Cloud Firewall** in the console, in front of `ufw` with the same two ports, is
  an optional second layer. It is left off so the setup stays host-neutral (D37).
