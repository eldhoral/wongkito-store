# Deploy

1. **VPS with a fixed IP.** Register the IP with itemku (Tokoku API) and whitelist it in Digiflazz.
2. **DNS.** A record `<domain>` -> VPS IP. TXT record `v-api-itemku-{SHOPNAME}=1` (itemku domain verification).
3. **Build.** `make build` -> `bin/wongkito`; copy to `/opt/wongkito/wongkito`.
4. **Env file** `/etc/wongkito.env` (mode 600). Variables: `ITEMKU_API_KEY`, `ITEMKU_SECRET`, `ITEMKU_SHOP_ID`,
   `DF_USERNAME`, `DF_API_KEY`, `DF_WEBHOOK_SECRET`, `TG_BOT_TOKEN`, `TG_CHAT_ID`, `ADMIN_PASSWORD_HASH`,
   `SESSION_SECRET`, `DB_PATH` (e.g. `/var/lib/wongkito/wongkito.db`), `LISTEN_ADDR` (default `:8080`), `DRY_RUN`.
   Admin password hash (bcrypt):
   ```
   htpasswd -bnBC 10 "" <pw> | tr -d ':\n'
   ```
   In a systemd env file, do not quote the hash; bcrypt hashes contain `$` and systemd does not expand it in `EnvironmentFile`.
5. **Caddy.** Put `deploy/Caddyfile` (with your domain) in `/etc/caddy/Caddyfile`.
6. **Service.** Copy `deploy/wongkito.service` to `/etc/systemd/system/`, then `systemctl enable --now wongkito`.
7. **Webhooks.**
   - itemku callback URL: `https://<domain>/hooks/itemku`
   - Digiflazz webhook URL: `https://<domain>/hooks/digiflazz`, with a secret equal to `DF_WEBHOOK_SECRET`.
8. **Dry run.** Set `DRY_RUN=true` for the first days; flip to `false` once the flow looks right.
9. **Backups.** Nightly cron, plus an off-box copy (rsync/rclone) of the result:
   ```
   sqlite3 $DB_PATH ".backup /var/backups/wongkito-$(date +%F).db"
   ```
   In crontab, escape `%` as `\%`.
