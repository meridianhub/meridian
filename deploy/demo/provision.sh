#!/usr/bin/env bash
# provision.sh — Provision a DigitalOcean Droplet for the Meridian demo environment.
#
# Requirements:
#   - Ubuntu 22.04+ fresh Droplet (run as root)
#   - Internet access for package installation
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/meridianhub/meridian/demo/deploy/demo/provision.sh | bash
#   # or:
#   bash provision.sh [--ssh-authorized-keys "ssh-ed25519 AAAA..."]
#
# The script is idempotent — safe to run multiple times.

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration (override via environment variables before running)
# ---------------------------------------------------------------------------

# Space-separated list of IPs allowed to SSH in (empty = allow from anywhere)
SSH_ALLOWED_IPS="${SSH_ALLOWED_IPS:-}"

# SSH public key(s) to add to the deploy user's authorized_keys
# Can also be passed via --ssh-authorized-keys flag
SSH_AUTHORIZED_KEYS="${SSH_AUTHORIZED_KEYS:-}"

# Username for the non-root deploy user
DEPLOY_USER="${DEPLOY_USER:-deploy}"

# Base directory for Meridian on the host
MERIDIAN_DIR="${MERIDIAN_DIR:-/opt/meridian}"

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
  case $1 in
    --ssh-authorized-keys)
      if [[ $# -lt 2 || -z "$2" ]]; then
        echo "error: --ssh-authorized-keys requires a value" >&2
        exit 1
      fi
      SSH_AUTHORIZED_KEYS="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# ---------------------------------------------------------------------------
# 1. System updates and essential packages
# ---------------------------------------------------------------------------

log "1/7 — Updating system packages..."

export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get upgrade -y -q \
  -o Dpkg::Options::="--force-confdef" \
  -o Dpkg::Options::="--force-confold"

apt-get install -y -q \
  ca-certificates \
  curl \
  gnupg \
  lsb-release \
  ufw \
  unattended-upgrades \
  apt-transport-https \
  software-properties-common \
  jq \
  git \
  htop

# ---------------------------------------------------------------------------
# 2. Docker CE
# ---------------------------------------------------------------------------

log "2/7 — Installing Docker CE..."

if ! command -v docker &>/dev/null; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  echo \
    "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
    https://download.docker.com/linux/ubuntu \
    $(. /etc/os-release && echo "${VERSION_CODENAME}") stable" \
    > /etc/apt/sources.list.d/docker.list

  apt-get update -q
  apt-get install -y -q \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-buildx-plugin \
    docker-compose-plugin

  systemctl enable --now docker
  log "  Docker installed."
else
  log "  Docker already installed, skipping."
fi

# ---------------------------------------------------------------------------
# 3. Deploy user
# ---------------------------------------------------------------------------

log "3/7 — Configuring deploy user '${DEPLOY_USER}'..."

if ! id "${DEPLOY_USER}" &>/dev/null; then
  useradd --create-home --shell /bin/bash --groups docker "${DEPLOY_USER}"
  log "  User '${DEPLOY_USER}' created."
else
  # Ensure user is in the docker group even if created previously
  usermod -aG docker "${DEPLOY_USER}"
  log "  User '${DEPLOY_USER}' already exists, ensured docker group membership."
fi

# Set up SSH authorized_keys for the deploy user
DEPLOY_HOME=$(getent passwd "${DEPLOY_USER}" | cut -d: -f6)
DEPLOY_SSH_DIR="${DEPLOY_HOME}/.ssh"
install -d -m 700 -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" "${DEPLOY_SSH_DIR}"
AUTHORIZED_KEYS_FILE="${DEPLOY_SSH_DIR}/authorized_keys"

if [[ -n "${SSH_AUTHORIZED_KEYS}" ]]; then
  # Add key if not already present
  if ! grep -qF "${SSH_AUTHORIZED_KEYS}" "${AUTHORIZED_KEYS_FILE}" 2>/dev/null; then
    echo "${SSH_AUTHORIZED_KEYS}" >> "${AUTHORIZED_KEYS_FILE}"
    log "  SSH authorized key added for '${DEPLOY_USER}'."
  else
    log "  SSH authorized key already present for '${DEPLOY_USER}'."
  fi
fi

chmod 600 "${AUTHORIZED_KEYS_FILE}" 2>/dev/null || true
chown "${DEPLOY_USER}:${DEPLOY_USER}" "${AUTHORIZED_KEYS_FILE}" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4. Firewall (UFW) — restrict inbound to SSH (specific IPs) + Cloudflare only
# ---------------------------------------------------------------------------

log "4/7 — Configuring UFW firewall..."

ufw --force reset

# Default policies
ufw default deny incoming
ufw default allow outgoing

# SSH: allow from specific IPs only, or from anywhere if none configured
if [[ -n "${SSH_ALLOWED_IPS}" ]]; then
  for ip in ${SSH_ALLOWED_IPS}; do
    log "  Allowing SSH from ${ip}"
    ufw allow from "${ip}" to any port 22 proto tcp
  done
else
  log "  No SSH_ALLOWED_IPS configured — allowing SSH from anywhere (consider restricting)"
  ufw allow 22/tcp
fi

# Fetch Cloudflare IPv4 and IPv6 ranges and allow HTTP/HTTPS from them
update_cloudflare_rules() {
  log "  Fetching Cloudflare IP ranges..."

  local cf_ipv4
  cf_ipv4=$(curl -fsSL https://www.cloudflare.com/ips-v4 2>/dev/null || true)
  local cf_ipv6
  cf_ipv6=$(curl -fsSL https://www.cloudflare.com/ips-v6 2>/dev/null || true)

  if [[ -z "${cf_ipv4}" && -z "${cf_ipv6}" ]]; then
    log "  WARNING: Failed to fetch Cloudflare IPs. HTTP/HTTPS rules NOT updated."
    return 0
  fi

  # Remove existing Cloudflare-added HTTP/HTTPS rules (comment-based identification)
  # UFW does not support comments natively, so we track ranges in a file and
  # delete/re-add when updating.
  local cf_rules_file="/etc/ufw/cloudflare_ranges.txt"

  if [[ -f "${cf_rules_file}" ]]; then
    log "  Removing stale Cloudflare rules..."
    while IFS= read -r range; do
      [[ -z "${range}" ]] && continue
      ufw delete allow from "${range}" to any port 80  2>/dev/null || true
      ufw delete allow from "${range}" to any port 443 2>/dev/null || true
    done < "${cf_rules_file}"
  fi

  # Write fresh range list
  printf '%s\n%s' "${cf_ipv4}" "${cf_ipv6}" > "${cf_rules_file}"

  # Add updated rules
  while IFS= read -r range; do
    [[ -z "${range}" ]] && continue
    ufw allow from "${range}" to any port 80  proto tcp
    ufw allow from "${range}" to any port 443 proto tcp
  done < "${cf_rules_file}"

  log "  Cloudflare HTTP/HTTPS rules updated."
}

update_cloudflare_rules

ufw --force enable
log "  UFW enabled."

# Cron job to refresh Cloudflare IPs weekly (runs as root, every Sunday at 03:00)
CRON_MARKER="# meridian-cloudflare-ufw"
CRON_CMD="0 3 * * 0 root ${MERIDIAN_DIR}/scripts/update-cloudflare-ufw.sh >> /var/log/cloudflare-ufw.log 2>&1 ${CRON_MARKER}"
if ! grep -qF "${CRON_MARKER}" /etc/cron.d/meridian-cloudflare 2>/dev/null; then
  cat > /etc/cron.d/meridian-cloudflare <<EOF
${CRON_CMD}
EOF
  chmod 644 /etc/cron.d/meridian-cloudflare
  log "  Cloudflare UFW refresh cron job installed."
fi

# Install the update script itself so the cron job can call it
install -d -m 755 "${MERIDIAN_DIR}/scripts"
cat > "${MERIDIAN_DIR}/scripts/update-cloudflare-ufw.sh" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

CF_RULES_FILE="/etc/ufw/cloudflare_ranges.txt"

CF_IPV4=$(curl -fsSL https://www.cloudflare.com/ips-v4 2>/dev/null || true)
CF_IPV6=$(curl -fsSL https://www.cloudflare.com/ips-v6 2>/dev/null || true)

# Abort if both lists are empty — do not wipe existing rules
if [[ -z "${CF_IPV4}" && -z "${CF_IPV6}" ]]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: Failed to fetch Cloudflare IP ranges. Existing rules unchanged." >&2
  exit 1
fi

NEW_RANGES=$(printf '%s\n%s' "${CF_IPV4}" "${CF_IPV6}")

# Guard: refuse to wipe if the new range list is suspiciously short
RANGE_COUNT=$(echo "${NEW_RANGES}" | grep -c '[0-9]' || true)
if [[ "${RANGE_COUNT}" -lt 5 ]]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: Cloudflare IP list has only ${RANGE_COUNT} entries; aborting to avoid wiping rules." >&2
  exit 1
fi

# Remove stale rules
if [[ -f "${CF_RULES_FILE}" ]]; then
  while IFS= read -r range; do
    [[ -z "${range}" ]] && continue
    ufw delete allow from "${range}" to any port 80  2>/dev/null || true
    ufw delete allow from "${range}" to any port 443 2>/dev/null || true
  done < "${CF_RULES_FILE}"
fi

echo "${NEW_RANGES}" > "${CF_RULES_FILE}"

while IFS= read -r range; do
  [[ -z "${range}" ]] && continue
  ufw allow from "${range}" to any port 80  proto tcp
  ufw allow from "${range}" to any port 443 proto tcp
done < "${CF_RULES_FILE}"

ufw reload
echo "$(date '+%Y-%m-%d %H:%M:%S') Cloudflare UFW rules updated (${RANGE_COUNT} ranges)."
SCRIPT
chmod 755 "${MERIDIAN_DIR}/scripts/update-cloudflare-ufw.sh"

# ---------------------------------------------------------------------------
# 5. Meridian directory structure
# ---------------------------------------------------------------------------

log "5/7 — Creating Meridian directory structure..."

install -d -m 750 "${MERIDIAN_DIR}"
install -d -m 700 "${MERIDIAN_DIR}/certs"
install -d -m 700 "${MERIDIAN_DIR}/secrets"
install -d -m 750 "${MERIDIAN_DIR}/data"
install -d -m 755 "${MERIDIAN_DIR}/scripts"

chown -R "${DEPLOY_USER}:${DEPLOY_USER}" "${MERIDIAN_DIR}"
log "  Created ${MERIDIAN_DIR}/{certs,secrets,data,scripts}"
log "  Place JWT signing key at ${MERIDIAN_DIR}/secrets/jwt-signing-key.pem (mode 600, deploy-owned)"

# ---------------------------------------------------------------------------
# 6. SSH hardening
# ---------------------------------------------------------------------------

log "6/7 — Hardening SSH configuration..."

SSHD_CUSTOM="/etc/ssh/sshd_config.d/99-meridian-hardening.conf"

# Write a drop-in config so we don't patch sshd_config directly
cat > "${SSHD_CUSTOM}" <<'EOF'
# Meridian demo hardening — managed by provision.sh
PasswordAuthentication no
ChallengeResponseAuthentication no
PermitRootLogin prohibit-password
PubkeyAuthentication yes
AuthorizedKeysFile .ssh/authorized_keys
PermitEmptyPasswords no
X11Forwarding no
AllowTcpForwarding no
EOF

chmod 644 "${SSHD_CUSTOM}"

# Validate and reload
sshd -t
systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true
log "  SSH hardening applied."

# ---------------------------------------------------------------------------
# 7. Unattended security upgrades
# ---------------------------------------------------------------------------

log "7/7 — Enabling unattended security upgrades..."

cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Download-Upgradeable-Packages "1";
APT::Periodic::AutocleanInterval "7";
APT::Periodic::Unattended-Upgrade "1";
EOF

cat > /etc/apt/apt.conf.d/50unattended-upgrades <<'EOF'
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}-security";
    "${distro_id}ESMApps:${distro_codename}-apps-security";
    "${distro_id}ESM:${distro_codename}-infra-security";
};
Unattended-Upgrade::AutoFixInterruptedDpkg "true";
Unattended-Upgrade::MinimalSteps "true";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-New-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "false";
EOF

systemctl enable --now unattended-upgrades
log "  Unattended upgrades enabled."

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

log ""
log "Provisioning complete."
log ""
log "Summary:"
log "  Droplet user : ${DEPLOY_USER}"
log "  Meridian dir : ${MERIDIAN_DIR}"
log "  Firewall     : UFW active — SSH + Cloudflare HTTP/HTTPS only"
log "  SSH          : password auth disabled, root login restricted"
log ""
log "Next steps:"
log "  1. Copy deploy/demo/docker-compose.yml to ${MERIDIAN_DIR}/docker-compose.yml"
log "  2. Copy deploy/demo/.env.demo.example to ${MERIDIAN_DIR}/.env and fill in secrets"
log "  3. Generate the JWT signing key (as ${DEPLOY_USER}):"
log "       openssl genrsa -out ${MERIDIAN_DIR}/secrets/jwt-signing-key.pem 2048"
log "       chmod 600 ${MERIDIAN_DIR}/secrets/jwt-signing-key.pem"
log "  4. Log in as '${DEPLOY_USER}' and run: cd ${MERIDIAN_DIR} && docker compose up -d"
log "  5. Add the deploy user's SSH private key as the DEPLOY_SSH_KEY GitHub secret"
