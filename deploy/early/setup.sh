#!/bin/bash
# Sets up a fresh Debian VM for the early private relay (ticket 4.1p).
# Runbook: Docs/ops/early-relay-deploy.md. Run as root from this directory:
#
#   bash setup.sh                         # SSH open to any address (key-only + fail2ban)
#   SSH_ALLOW_FROM=203.0.113.7 bash setup.sh   # SSH only from your static IP
#
# Safe to run again: every step checks or overwrites with the same content.
# Downloads nothing except Debian packages through apt. The relay binary must
# already be at /opt/agentnet-relay/relay (you upload it, see the runbook).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
bin=/opt/agentnet-relay/relay

step() { printf '\n== %s\n' "$*"; }
die() { printf 'setup.sh: %s\n' "$*" >&2; exit 1; }
# Copies a config file with Windows line endings removed (a CR would end up
# inside systemd values such as User=).
put() { tr -d '\r' <"$1" >"$2.tmp" && chmod 0644 "$2.tmp" && mv "$2.tmp" "$2"; }

[ "$(id -u)" -eq 0 ] || die "run as root"
[ -r /etc/debian_version ] || die "this script expects Debian (or Ubuntu)"
for f in Caddyfile agentnet-relay.service; do
	[ -r "$here/$f" ] || die "missing $here/$f"
done

# Refuse to turn off SSH passwords unless root can already log in with a key,
# so this script cannot lock you out.
if ! grep -qsE '(ssh|ecdsa|sk)-[A-Za-z0-9@.-]+ AAAA' /root/.ssh/authorized_keys; then
	die "no SSH key in /root/.ssh/authorized_keys; add your key before running this"
fi

step "Packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q ufw fail2ban python3-systemd unattended-upgrades caddy

step "SSH: keys only"
cat >/etc/ssh/sshd_config.d/10-agentnet-relay.conf <<'EOF'
# Written by deploy/early/setup.sh (ticket 4.1p).
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
EOF
sshd -t
systemctl reload ssh

step "fail2ban for sshd"
# Debian 12 has no /var/log/auth.log by default: read the journal instead.
cat >/etc/fail2ban/jail.d/10-agentnet-relay.local <<'EOF'
# Written by deploy/early/setup.sh (ticket 4.1p).
[sshd]
enabled = true
backend = systemd
EOF
systemctl enable fail2ban
systemctl restart fail2ban

step "Firewall: 22/tcp and 443/tcp in, nothing else"
ufw default deny incoming
ufw default allow outgoing
if [ -n "${SSH_ALLOW_FROM:-}" ]; then
	ufw allow from "$SSH_ALLOW_FROM" to any port 22 proto tcp
	# Drop a rule for SSH from anywhere left by an earlier run without SSH_ALLOW_FROM.
	ufw delete allow 22/tcp >/dev/null 2>&1 || true
else
	ufw allow 22/tcp
fi
ufw allow 443/tcp
ufw --force enable
ufw status verbose

step "Automatic security updates"
cat >/etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
// Written by deploy/early/setup.sh (ticket 4.1p).
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
cat >/etc/apt/apt.conf.d/52agentnet-relay-reboot <<'EOF'
// Written by deploy/early/setup.sh (ticket 4.1p). Reboot for kernel and libc
// updates at a quiet hour; the relay restarts on boot and daemons reconnect.
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:00";
EOF
systemctl enable --now unattended-upgrades

step "Logs: keep 14 days at most (relay-hosted.md section 5)"
mkdir -p /etc/systemd/journald.conf.d
cat >/etc/systemd/journald.conf.d/10-agentnet-relay.conf <<'EOF'
# Written by deploy/early/setup.sh (ticket 4.1p).
[Journal]
MaxRetentionSec=14day
EOF
systemctl restart systemd-journald

step "Service user and relay binary"
if ! id agentnet-relay >/dev/null 2>&1; then
	useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin agentnet-relay
fi
install -d -m 0755 -o root -g root /opt/agentnet-relay
if [ -f "$bin" ]; then
	chown root:root "$bin"
	chmod 0755 "$bin"
	"$bin" --version
else
	echo "relay binary not found at $bin: upload it (runbook step 4) and run this again"
fi

step "Caddy and the relay unit"
if [ -f /etc/caddy/Caddyfile ] && [ ! -e /etc/caddy/Caddyfile.debian-default ]; then
	cp -p /etc/caddy/Caddyfile /etc/caddy/Caddyfile.debian-default
fi
put "$here/Caddyfile" /etc/caddy/Caddyfile
caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile
put "$here/agentnet-relay.service" /etc/systemd/system/agentnet-relay.service
if [ -x "$bin" ]; then
	# Complains about a missing ExecStart binary, so only once it is there.
	systemd-analyze verify /etc/systemd/system/agentnet-relay.service
fi
systemctl daemon-reload
systemctl enable caddy
systemctl restart caddy
if [ -x "$bin" ]; then
	systemctl enable agentnet-relay
	systemctl restart agentnet-relay
	sleep 2
	systemctl --no-pager --lines=5 status agentnet-relay || true
else
	echo "agentnet-relay not started: no binary yet"
fi

step "Done. Next: the checks in Docs/ops/early-relay-deploy.md, step 7."
