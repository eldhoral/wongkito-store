# Task 20: Sandbox and dry-run verification log

**Status: NOT RUN.** Manual verification against real services. Requires itemku sandbox keys, a registered fixed IP, and Digiflazz credentials.

## Prerequisites

- VPS with a fixed IP. Register the IP with itemku (Tokoku API) and whitelist it in Digiflazz.
- DNS: A record `<domain>` -> VPS IP. TXT record `v-api-itemku-{SHOPNAME}=1` (itemku domain verification).
- Env file `/etc/wongkito.env` (mode 600) with `ITEMKU_API_KEY`, `ITEMKU_SECRET`, `ITEMKU_SHOP_ID`, `DF_USERNAME`, `DF_API_KEY`, `DF_WEBHOOK_SECRET`, `TG_BOT_TOKEN`, `TG_CHAT_ID`, `ADMIN_PASSWORD_HASH`, `SESSION_SECRET`, `DB_PATH`, `LISTEN_ADDR`, `DRY_RUN`.
- Webhooks: itemku callback URL `https://<domain>/hooks/itemku`; Digiflazz webhook URL `https://<domain>/hooks/digiflazz` with secret equal to `DF_WEBHOOK_SECRET`.
- Start with `DRY_RUN=true`; flip to `false` only once the flow looks right.

## Step 1: itemku sandbox

- [ ] **Step 1: itemku sandbox.** Keys come from itemku Discord; the IP must be registered. Run the binary with `ITEMKU_BASE_URL=https://sandbox-api-tokoku.itemku.com` and `DRY_RUN=true`. `POST /dev/api/product/generate`, then `/dev/api/order/generate` (`game_id 51`, `item_type_id 95`). Expected: the poll picks it up within 30 s, and it appears in Queue as `no_mapping`.

Date:
Run by:
Result: PASS / FAIL
Notes:

## Step 2: Link the sandbox product

- [ ] **Step 2: Link the sandbox product** to a Digiflazz SKU and set the template. Retry in the UI → the Digiflazz call is sent with `testing:true` → `Sukses` → the DELIVER call is logged and not sent (dry run). Then repeat with `DRY_RUN=false` against the sandbox → itemku order status `DELIVERED`.

Date:
Run by:
Result: PASS / FAIL
Notes:

## Step 3: Digiflazz webhook

- [ ] **Step 3: Digiflazz webhook.** Trigger a `testing:true` transaction that returns `Pending` (use the Digiflazz test-case customer numbers) and confirm the webhook moves it to delivered. Also confirm a tampered signature returns 401 in the `api_log`.

Date:
Run by:
Result: PASS / FAIL
Notes:

## Step 4: Production, `DRY_RUN=true`

- [ ] **Step 4: Production, `DRY_RUN=true`.**
  - DNS TXT, callback URL, Digiflazz IP whitelist and webhook set
  - one real cheap order arrives via the callback (`last_callback` updates)
  - the converted number is correct
  - no money moves

Date:
Run by:
Result: PASS / FAIL
Notes:

## Step 5: Production live

- [ ] **Step 5: Production live.** Set `DRY_RUN=false`, put one product in Auto, and place one real order. Check: Digiflazz `Sukses`, itemku `DELIVERED`, no Telegram alert, margin correct in History. Then enable the rest gradually.

Date:
Run by:
Result: PASS / FAIL
Notes:
