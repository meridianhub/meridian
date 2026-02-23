# Cloudflare Setup Checklist — Demo Environment

This checklist covers the one-time Cloudflare configuration required to serve the Meridian demo
environment at `demo.meridianhub.cloud`. Complete every section in order before deploying services
to the Droplet.

---

## Prerequisites

- Access to the `meridianhub.cloud` zone in Cloudflare (zone owner or Admin role).
- The public IPv4 address of the demo Droplet (referred to below as `<DROPLET_IP>`).
- SSH access to the Droplet for placing certificate files.

---

## 1. DNS Records

Navigate to **Cloudflare Dashboard → meridianhub.cloud → DNS → Records**.

Add the following A records:

| Type | Name                    | Content        | Proxy status | TTL  |
|------|-------------------------|----------------|--------------|------|
| A    | `demo`                  | `<DROPLET_IP>` | Proxied      | Auto |
| A    | `*.demo`                | `<DROPLET_IP>` | Proxied      | Auto |

> **Why both?** The apex `demo` record handles `demo.meridianhub.cloud` itself (e.g., health
> checks, redirect). The wildcard `*.demo` record routes all tenant subdomains such as
> `acme.demo.meridianhub.cloud` to the same Droplet. Cloudflare proxies both so the Droplet IP
> remains hidden.

### Future environment patterns

Replicate this two-record pattern for additional environments:

| Environment | Apex record   | Wildcard record   |
|-------------|---------------|-------------------|
| staging     | `staging`     | `*.staging`       |
| production  | (root `@`)    | `*`               |

---

## 2. Origin Certificate

Cloudflare Origin Certificates allow the Droplet to terminate TLS using a certificate issued
directly by Cloudflare, without involving a public CA. This is required when SSL mode is set to
**Full (Strict)**.

### 2.1 Generate the certificate

1. Navigate to **Cloudflare Dashboard → meridianhub.cloud → SSL/TLS → Origin Server**.
2. Click **Create Certificate**.
3. Select **Let Cloudflare generate a private key and a CSR** (recommended).
4. Under **Hostnames**, ensure the following are listed:
   - `*.demo.meridianhub.cloud`
   - `demo.meridianhub.cloud`
5. Set **Key type** to **RSA (2048)**.
6. Set **Certificate Validity** to **15 years**.
7. Click **Create**.
8. Copy the **Origin Certificate** (PEM block beginning with `-----BEGIN CERTIFICATE-----`).
9. Copy the **Private Key** (PEM block beginning with `-----BEGIN RSA PRIVATE KEY-----`).

> **Warning:** The private key is shown only once. Copy it before closing the dialog.

### 2.2 Place files on the Droplet

SSH into the Droplet and run:

```bash
sudo mkdir -p /opt/meridian/certs
sudo chmod 700 /opt/meridian/certs
```

Write the certificate:

```bash
sudo tee /opt/meridian/certs/origin-cert.pem <<'EOF'
<paste Origin Certificate PEM here>
EOF
```

Write the private key:

```bash
sudo tee /opt/meridian/certs/origin-key.pem <<'EOF'
<paste Private Key PEM here>
EOF
sudo chmod 600 /opt/meridian/certs/origin-key.pem
```

Verify the files are in place:

```bash
ls -la /opt/meridian/certs/
# Expected:
# -rw------- ... origin-key.pem
# -rw-r--r-- ... origin-cert.pem
```

The Caddy / Nginx configuration for the Droplet should reference these paths directly.

---

## 3. SSL/TLS Settings

Navigate to **Cloudflare Dashboard → meridianhub.cloud → SSL/TLS**.

### 3.1 Encryption mode

| Setting          | Value           |
|------------------|-----------------|
| SSL/TLS mode     | **Full (Strict)** |

> Full (Strict) validates the Origin Certificate on the Droplet. Do not use "Full" (without Strict)
> or "Flexible", as these either skip validation or terminate TLS at Cloudflare without re-encrypting
> to the origin.

### 3.2 Edge Certificates tab

| Setting                    | Value  |
|----------------------------|--------|
| Minimum TLS Version        | **1.2** |
| Always Use HTTPS           | **On** |
| Automatic HTTPS Rewrites   | **On** |

> TLS 1.0 and 1.1 are cryptographically broken. Enforcing 1.2 as the minimum is the current
> industry baseline; 1.3 is preferred where client compatibility allows.

---

## 4. Security Settings

Navigate to **Cloudflare Dashboard → meridianhub.cloud → Security**.

### 4.1 Bot Fight Mode (all plans)

| Setting        | Value |
|----------------|-------|
| Bot Fight Mode | **On** |

Bot Fight Mode challenges known bot traffic at the Cloudflare edge before it reaches the Droplet,
reducing noise in application logs and protecting rate-limited endpoints.

### 4.2 WAF Rules (Pro plan and above)

If the zone is on a Cloudflare Pro plan or higher, configure the following under
**Security → WAF → Managed Rules**:

| Rule set                     | Action    |
|------------------------------|-----------|
| Cloudflare Managed Ruleset   | Block     |
| Cloudflare OWASP Core Ruleset | Block     |

For the demo environment, the OWASP paranoia level can remain at **PL1** (low false-positive rate).
Increase to PL2 for staging and production after confirming no legitimate traffic is blocked.

### 4.3 Rate Limiting (Pro plan and above)

Recommended rate-limit rules for the demo environment:

| Rule name              | Expression                                      | Threshold   | Period | Action  |
|------------------------|--------------------------------------------------|-------------|--------|---------|
| API general            | `http.request.uri.path matches "^/api/"`         | 1000 req    | 1 min  | Block   |
| Auth endpoints         | `http.request.uri.path matches "^/api/auth/"`    | 20 req      | 1 min  | Block   |
| Health check bypass    | `http.request.uri.path eq "/healthz"`            | (skip rule) | —      | Skip    |

> On the free plan, rate limiting is not available. Consider enabling it when promoting to
> staging or production.

---

## 5. Subdomain Convention

All Meridian environments follow the pattern:

```text
<tenant>.<env>.meridianhub.cloud
```

| Component | Description                                  | Example        |
|-----------|----------------------------------------------|----------------|
| `tenant`  | Short identifier for the customer or team    | `acme`, `globex` |
| `env`     | Environment name                             | `demo`, `staging`, `production` |

### Examples

| Subdomain                            | Description                       |
|--------------------------------------|-----------------------------------|
| `acme.demo.meridianhub.cloud`        | ACME tenant on demo environment   |
| `globex.demo.meridianhub.cloud`      | Globex tenant on demo environment |
| `acme.staging.meridianhub.cloud`     | ACME tenant on staging            |
| `acme.meridianhub.cloud`             | ACME tenant on production         |

> For production, tenants sit directly under `meridianhub.cloud` (no `production.` infix) to keep
> URLs short. This requires a production-specific wildcard record on the root zone.

### Tenant provisioning

When a new tenant is onboarded to the demo environment:

1. No DNS changes are required — the `*.demo` wildcard record already covers new subdomains.
2. The provisioning script (`deploy/demo/provision.sh`) creates the Caddy virtual-host block and
   restarts the reverse proxy.
3. Cloudflare proxies the new subdomain automatically on the next request.

---

## 6. Verification

### 6.1 DNS propagation

Check that both A records resolve through Cloudflare's proxy:

```bash
# Confirm apex resolves (should return Cloudflare anycast IPs, not the Droplet IP)
dig +short demo.meridianhub.cloud A

# Confirm wildcard resolves
dig +short acme.demo.meridianhub.cloud A

# Both should return Cloudflare anycast addresses (e.g. 104.x.x.x or 172.x.x.x)
```

If propagation is still in progress, the following will return `NXDOMAIN` or the old IP — wait a
few minutes and retry.

### 6.2 Cloudflare proxy headers

Verify that requests pass through the Cloudflare edge:

```bash
curl -sI https://demo.meridianhub.cloud | grep -i "cf-ray\|server\|x-cache"
# Expected headers:
# cf-ray: <ray-id>
# server: cloudflare
```

A `cf-ray` header confirms the request was handled by the Cloudflare edge. Absence of this header
means the proxy is disabled (orange cloud is off) or DNS has not yet propagated.

### 6.3 Origin Certificate validation

Verify that the Droplet is presenting the correct certificate and that Cloudflare accepts it:

```bash
# From any machine — tests the full Cloudflare-to-origin TLS chain
curl -sv https://demo.meridianhub.cloud 2>&1 | grep -E "subject|issuer|expire|SSL"

# The certificate issuer should be:
# issuer: O=Cloudflare, Inc.; CN=Cloudflare Origin CA RSA Root
```

If you see a certificate issued by Let's Encrypt or another public CA instead, the Origin
Certificate has not been installed correctly on the Droplet.

### 6.4 SSL mode confirmation

Confirm Full (Strict) mode is active by checking there is no certificate error for an invalid
origin certificate:

```bash
# This should return a valid TLS response, NOT a 526 (Invalid SSL Certificate) error
curl -o /dev/null -w "%{http_code}" https://demo.meridianhub.cloud/healthz
# Expected: 200 (or 301/302 if redirecting)
```

A `526` response means the Origin Certificate is missing or expired on the Droplet.

---

## Troubleshooting

| Symptom | Likely cause | Resolution |
|---------|--------------|------------|
| `526 Invalid SSL Certificate` | Origin cert not installed | Re-run Section 2.2 |
| `525 SSL Handshake Failed` | Caddy/Nginx not configured for TLS | Ensure origin server listens on port 443 |
| `cf-ray` header missing | DNS proxy disabled | Enable orange cloud on both DNS records |
| Wildcard not resolving | Missing `*.demo` record | Add wildcard A record (Section 1) |
| WAF blocking legitimate traffic | Overly strict rules | Lower OWASP paranoia level to PL1 |
