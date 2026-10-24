# Deploy

1. **VPS with a fixed IP.** Register the IP with itemku (Tokoku API) and whitelist it in Digiflazz.
2. **DNS.** A record `<domain>` -> VPS IP. TXT record `v-api-itemku-{SHOPNAME}=1` (itemku domain verification).
3. **Build.** Pushes to `main` build and deploy automatically (see "CI/CD" below). Manual fallback:
   `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 make build` -> `bin/wongkito`; copy to `/opt/wongkito/wongkito`.
4. **Env file** `/etc/wongkito.env` (mode 600). Variables: `ITEMKU_API_KEY`, `ITEMKU_SECRET`, `ITEMKU_SHOP_ID`,
   `DF_USERNAME`, `DF_API_KEY`, `DF_WEBHOOK_SECRET`, `TG_BOT_TOKEN`, `TG_CHAT_ID`, `ADMIN_PASSWORD_HASH`,
   `SESSION_SECRET`, `DB_PATH` (e.g. `/var/lib/wongkito/wongkito.db`), `LISTEN_ADDR` (default `127.0.0.1:8080`), `DRY_RUN`.
   Admin password hash (bcrypt):
   ```
   htpasswd -bnBC 10 "" <pw> | tr -d ':\n'
   ```
   In a systemd env file, do not quote the hash; bcrypt hashes contain `$` and systemd does not expand it in `EnvironmentFile`.
5. **Caddy.** Put `deploy/Caddyfile` (with your domain) in `/etc/caddy/Caddyfile`.
6. **Service.** Copy `deploy/wongkito.service` to `/etc/systemd/system/`, then `systemctl enable wongkito`.
   The first CI deploy uploads the binary and starts it.
7. **Webhooks.**
   - itemku callback URL: `https://<domain>/hooks/itemku`
   - Digiflazz webhook URL: `https://<domain>/hooks/digiflazz`, with a secret equal to `DF_WEBHOOK_SECRET`.
8. **Dry run.** Set `DRY_RUN=true` for the first days; flip to `false` once the flow looks right.
9. **Backups.** Nightly cron, plus an off-box copy (rsync/rclone) of the result:
   ```
   sqlite3 $DB_PATH ".backup /var/backups/wongkito-$(date +%F).db"
   ```
   In crontab, escape `%` as `\%`.

## Oracle Cloud VM (Always Free)

1. **Instance.** Home region Singapore. Shape `VM.Standard.A1.Flex` (Ampere, ARM), Ubuntu, 1 OCPU / 6 GB is plenty.
   Add your own SSH public key at creation.
2. **Fixed IP.** Networking → Reserved Public IPs → reserve one and attach it to the instance's VNIC.
   This is the IP you register with itemku and whitelist in Digiflazz.
3. **Ingress.** In the VCN security list allow TCP 80 and 443 from `0.0.0.0/0`. Ubuntu images on OCI also
   firewall with iptables, so open the ports on the VM too:
   ```
   sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 80 -j ACCEPT
   sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
   sudo netfilter-persistent save
   ```
4. **Idle reclaim.** Oracle may reclaim Always Free VMs that stay nearly idle for 7 days. Upgrading the account to
   Pay As You Go removes that risk; usage inside the Always Free limits stays free.
5. **Packages.** `sudo apt update && sudo apt install -y caddy sqlite3 curl` (if `caddy` is missing, add Caddy's apt
   repo from caddyserver.com/docs/install).

## CI/CD (GitHub Actions)

`.github/workflows/deploy.yml` runs `make test` and `go vet` on every push and pull request. On a push to `main` it
also builds the linux/arm64 binary, uploads it over SSH, restarts the service, checks
`http://127.0.0.1:8080/`, and rolls back to the previous binary if the check fails.

One-time setup on the VM:

```
# service user (runs the app) and data dir
sudo useradd --system --home /var/lib/wongkito --shell /usr/sbin/nologin wongkito
sudo install -d -o wongkito -g wongkito -m 700 /var/lib/wongkito

# deploy user (CI logs in as this); owns the binary dir, the service can read/exec it
sudo useradd --create-home --shell /bin/bash deploy
sudo install -d -o deploy -g wongkito -m 750 /opt/wongkito

# deploy may restart the service and nothing else
echo 'deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart wongkito' | sudo tee /etc/sudoers.d/wongkito-deploy
sudo chmod 440 /etc/sudoers.d/wongkito-deploy && sudo visudo -c

# CI key: generate on your laptop, never on the VM
#   ssh-keygen -t ed25519 -N '' -C wongkito-ci -f wongkito-ci
sudo install -d -o deploy -g deploy -m 700 /home/deploy/.ssh
# paste the contents of wongkito-ci.pub into:
sudo -u deploy tee /home/deploy/.ssh/authorized_keys
sudo chmod 600 /home/deploy/.ssh/authorized_keys
```

Then steps 4–6 above (env file, Caddy, `systemctl enable wongkito`). Keep `LISTEN_ADDR` at the default
`127.0.0.1:8080`; the health check and the Caddyfile assume it.

GitHub → repo Settings → Secrets and variables → Actions → New repository secret:

| Secret | Value |
|---|---|
| `DEPLOY_HOST` | the reserved public IP |
| `DEPLOY_USER` | `deploy` |
| `DEPLOY_SSH_KEY` | contents of the private key `wongkito-ci` |
| `DEPLOY_KNOWN_HOSTS` | output of `ssh-keyscan -t ed25519 <IP>`, run after you have logged in once and confirmed the host fingerprint |

App secrets (`ITEMKU_*`, `DF_*`, `TG_*`, `ADMIN_PASSWORD_HASH`, `SESSION_SECRET`) stay only in `/etc/wongkito.env`
on the VM; never add them to GitHub. Push to `main` to deploy; watch it under the repo's Actions tab.
