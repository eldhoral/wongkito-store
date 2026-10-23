# itemku ⇄ Digiflazz Relay — Design

Date: 2026-10-23
Status: draft, awaiting owner approval

## 1. Intent

The owner runs a top-up store on itemku (game top-ups such as Mobile Legends and Free Fire, plus pulsa, data and e-wallet). Every order is fulfilled by buying the same item on Digiflazz as a buyer and then marking the itemku order as delivered. Today this is all done by hand, 50–100 orders a day, mostly from a phone.

The most painful manual step is reformatting the buyer's game ID. itemku gives separate fields (e.g. `player_id` and `zone_id`), but each Digiflazz seller wants its own format (`12345678|1234` for one seller, `123456781234` for another).

This app receives itemku orders, converts the buyer data into the right Digiflazz customer number, buys on Digiflazz, and closes the itemku order. Each product can run in **Auto** mode (no human touch) or **Manual** mode (owner confirms with one tap), or be switched **Off** on itemku.

## 2. Goals

- Receive every paid itemku order within seconds, and never miss one.
- Convert itemku `required_information` into the Digiflazz `customer_no` using a template stored per **(Digiflazz seller, itemku game)** pair.
- Per itemku product: **Off / Manual / Auto**.
  - Auto: buy on Digiflazz, and on `Sukses` deliver on itemku, with no human involved.
  - Manual: the order waits in the queue; the owner checks the converted number, can edit it or pick another seller, then taps "Buy & deliver".
  - Off: product stock set to 0 on itemku so buyers cannot order it.
- An Auto order that fails at Digiflazz moves to the manual queue. It is **not** auto-refunded. The owner retries, possibly with another seller's SKU, or rejects and refunds.
- Automatically turn a product Off when its Digiflazz product is unavailable or costs at least the itemku price. Turn it back on when the cause clears, but only if the app was the one that turned it off.
- Telegram alerts for things that need the owner.
- Phone-first UI (installable PWA), usable on a laptop too.
- **Direct orders:** buyers who message the owner on WhatsApp. The owner enters the order in the app, which buys on Digiflazz with the same templates and safety rules, then hands over a ready WhatsApp message with the SN.

## 3. Non-goals

- Multiple itemku shops, multiple users or staff roles.
- Automatic price updates on itemku (the API supports it; not requested).
- Voucher or account products that need `delivery_info`. Only top-up-style products are in scope (`using_delivery_info: false`).
- Postpaid (pasca) Digiflazz products.
- A public shop website where buyers order and pay by themselves (payment gateway). The owner wants this **later**, as a separate project. Nothing here needs to be built for it, but the order/attempt model should not assume every order comes from itemku.
- Payment tracking for direct orders. The owner creates a direct order only after the buyer's money has arrived.
- Accounting and reporting beyond per-order cost and margin and a simple history list.

## 4. Anti-goals (failure even if it "works")

- **Buying twice for one order unit.** This loses real money. Duplicate itemku callbacks, retries, timeouts and pending states must never cause a second purchase of a unit that already succeeded or is still pending.
- **Marking an itemku order delivered when the top-up did not succeed.**
- **Sending a wrongly formatted ID in Auto mode** because the seller behind a SKU is not the one the template was chosen for.
- Silent failure: an order stuck with nobody told.
- API keys reachable from the browser.

## 5. External APIs (verified from docs on 2026-10-23)

### 5.1 itemku — "Tokoku Open API"

Source: https://files.itemku.com/document/Tokoku+API+Docs.pdf (endpoints updated Nov 3, 2025)

- Base: `https://tokoku-gateway.itemku.com`. Sandbox: `https://sandbox-api-tokoku.itemku.com` (separate keys, register via itemku Discord, IP must be registered).
- All calls are `POST` with JSON. Calls are accepted **only from registered IPs**, so the server needs a fixed public IP.
- Auth headers: `X-Api-Key`, `Content-Type: application/json`, `Nonce` (current unix timestamp string), `Authorization: Bearer <token>`.
  - Token = JWT-like: `base64url(header) + "." + base64url(payload)`, then HMAC-SHA256 with the API Secret, joined with `.`.
  - Header JSON: `{"X-Api-Key":"…","Nonce":"…","alg":"HS256"}`. Payload JSON = the exact request body.
  - Both must have `\t \r \n` stripped and all values trimmed. HTTP 401 means a bad key, nonce drift, wrong content type, a bad token, or a body that differs from the signed payload.
- `POST /api/order/list`: `date_start` (YYYY-MM-DD, max 60 days back, required), optional `order_id`, `order_status` (`REQUIRE_PROCESS` | `DELIVERED` | `REFUNDED`), `limit` 1–30 (default 10). **No pagination parameter.** Returns orders sorted desc.
  - Order fields used: `order_id`, `order_number`, `product_id`, `price`, `quantity`, `order_income`, `game_name`, `product_name`, `status`, `required_information` (a **JSON string**, e.g. `"{\"player_id\":\"12345567\",\"zone_id\":\"1234\"}"`), `using_delivery_info`, `order_created_at`, `buyer_name`, `category_name`.
- `POST /api/order/action`: `order_id`, `action` = `DELIVER` | `REJECT`.
  - Top-up products send no `delivery_info`.
  - `REJECT` requires `refund_reason`, one of: `NO_STOCK`, `WRONG_BUYER_INFORMATION`, `SERVER_ERROR`, `PRICE_CHANGED`, `BUYER_UNREACHABLE`, … (see PDF).
  - Errors: `DELIVER_ORDER_IN_PROGRESS`, `ORDER_DELIVERED`, `ORDER_REJECTED`, `MISSMATCH_STATUS`, `INVALID_ORDER_INFO`, `REFUND_FAILED`.
  - **Docs warning:** a timeout or a non-200 response means the order may or may not have been updated.
- **Order callback** (production only, not in sandbox):
  - itemku POSTs `{"data":[order…]}` to a seller-provided URL when an order is paid, with header `X-itemku: {shop_id}`.
  - There is no signature. Verify by calling `order/list` with the `order_id` and checking that its status is `REQUIRE_PROCESS`.
  - Requires a DNS TXT record `v-api-itemku-{SHOPNAME}=1` on the callback domain.
  - On non-2xx or a network error, itemku retries up to 3 times (5s, 30s, 2min) and then stops. Processing must be idempotent on `order_id`.
- `POST /api/product/list`: `id`, `game_id`, `is_out_of_stock`, `page`, max 10 per page. Returns `id`, `name`, `game_name`, `item_type_name`, `price`, `stock`, …
- `POST /api/product/stock/update`: `product_id`, `new_stock` (max 99999999). Not allowed for "auto delivery" products (`UPDATE_STOCK_REJECTED`). This is the **only way to switch a product Off**: stock 0.
- `POST /api/product/search-categories` (games), `POST /api/product/price/update` (not used).

### 5.2 Digiflazz — buyer API

Source: https://developer.digiflazz.com/api/buyer/

- `POST https://api.digiflazz.com/v1/transaction`: `username`, `buyer_sku_code`, `customer_no`, `ref_id` (unique, ours), `sign = md5(username + apiKey + ref_id)`, optional `testing`, `max_price`, `cb_url`, `allow_dot`.
  - Response `data`: `ref_id`, `customer_no`, `buyer_sku_code`, `message`, `status` (`Sukses` | `Pending` | `Gagal`), `rc`, `sn`, `buyer_last_saldo`, `price`, `tele`, `wa`.
  - **Re-sending the same `ref_id` returns the current status of that transaction**; it does not create a new purchase. This is the status-check mechanism.
- **The transaction response does not identify the seller.** The seller for a `buyer_sku_code` is chosen by the owner in the Digiflazz dashboard.
- `POST /v1/price-list` with `cmd: "prepaid"` and `sign = md5(username + apiKey + "pricelist")`.
  - Returns per product: `product_name`, `category`, `brand`, `type`, `seller_name`, `price`, `buyer_sku_code`, `buyer_product_status`, `seller_product_status`, `unlimited_stock`, `stock`, `multi`, `start_cut_off`, `end_cut_off`, `desc`.
  - Rate-limited (limit not published). Data can be 10–15 min stale. Digiflazz recommends caching it in our DB.
- Webhook: configured once in the Digiflazz dashboard (Webhook tab, with a secret).
  - Headers: `X-Digiflazz-Event` (`create` | `update`) and `X-Hub-Signature: sha1=<hex HMAC-SHA1(raw body, secret)>`.
  - The prepaid payload `data` has `ref_id`, `status`, `rc`, `sn`, `price`, ….
- Balance: the "cek saldo" endpoint (`/v1/cek-saldo`, `cmd: "deposit"`). The builder must confirm the exact sign string from the docs.

## 6. Decisions made with the owner

- **Stack:** Go backend + React frontend in **one monorepo**. Owner does not want PHP. Owner will maintain the code.
- **Templates are per (Digiflazz seller, itemku game).** One seller sells many games and uses different formats per game.
- **Auto failure goes to the manual queue**, never to an automatic refund.
- **Off = itemku stock 0.** Turning a product back on restores the stock it had before.
- **Auto turn-off** when the Digiflazz product is unavailable or the cost leaves no margin on **what the owner actually receives after itemku's fee** (not the list price), with automatic turn-back-on in the conditions in §8.4.
- **itemku lowers stock on every sale.** Some of the owner's products are itemku **"Pengiriman Otomatis"** (must be delivered within ~2 minutes) or **"Pengiriman 10 menit"** (within 10 minutes). Regular products: itemku auto-cancels after about 24 hours, or earlier if the buyer cancels. Stock updates are rejected for "Pengiriman Otomatis" products.
- **Alerts via Telegram.**
- **Direct WhatsApp orders** as described in §8.8 (owner approved the flow).
- **UI direction "B · One at a time"** (light, calm, oldest order shown as a large actionable card). Mockups in `docs/superpowers/specs/mockups/` (`queue-direction-b.html`: the right-hand phone is the chosen one; `products.html`).

## 7. Architecture

```
itemku ──callback──▶ ┌────────────────────────────┐ ◀──webhook── Digiflazz
        ◀─order/action│  api (Go, single binary)   │──transaction─▶
        ◀─stock/update│  SQLite file               │──price-list──▶
                      │  goroutine loops           │──Telegram────▶
                      │  serves web/dist (embedded)│
                      └────────────▲───────────────┘
                                   │ HTTPS (Caddy, auto TLS)
                             phone / laptop (PWA)
```

- **One VPS with a fixed public IP**, registered with itemku and whitelisted at Digiflazz. Builder's choice of provider, Jakarta/Singapore region preferred.
- **Caddy** in front for HTTPS. The Go binary listens on localhost and runs under systemd.
- **Backend (`api/`):** Go, stdlib `net/http` (1.22+ routing), `modernc.org/sqlite` (no cgo), `database/sql` (sqlc optional). Embedded SQL migrations.
  - Background work is goroutines with tickers: order poll, pending re-check, price-list sync, balance check.
  - No framework, no Redis, no job library.
- **Frontend (`web/`):** React + Vite + TypeScript + Tailwind. TanStack Query, polling every ~5 s on the Queue screen.
  - Radix/Vaul (or equivalent headless primitives) for the bottom sheet and menus.
  - Installable PWA (manifest + icons). No offline support needed.
  - The built `web/dist` is embedded into the Go binary, so deployment is one file.
- **Monorepo layout:** `api/`, `web/`, `docs/`, root `Makefile` (`make dev`, `make build`, `make deploy`). No Turborepo or Nx.
- **Secrets in env vars:** itemku key and secret, Digiflazz username, key and webhook secret, Telegram bot token and chat id, admin password hash, session secret. Never sent to the browser.

## 8. Core behaviour

### 8.1 Order intake (idempotent)

1. **Callback** `POST /hooks/itemku`.
   - Check the `X-itemku` header equals our shop id.
   - Respond 200 immediately, then process each `order_id` asynchronously.
   - Processing: fetch the order via `order/list` (`order_id`); continue only if `status = REQUIRE_PROCESS`.
   - Insert into `orders` keyed by the itemku `order_id` (PRIMARY KEY). An existing row is a no-op.
2. **Backup poll** every 60 s: `order/list` with `order_status=REQUIRE_PROCESS`, `date_start` = 2 days ago, `limit=30`. Insert any unknown orders the same way.
   - Ceiling: no pagination, so at most 30 open orders are visible per poll. That is fine at this volume. Alert if 30 are returned.
3. Products with `using_delivery_info = true` go straight to the queue, flagged "unsupported product type".

### 8.2 Customer-number templates

- A template is a string with placeholders naming keys of `required_information`, e.g. `{player_id}|{zone_id}`, `{player_id}{zone_id}`, `{user_id}`.
- Values are trimmed. Two filters:
  - `{key|digits}` keeps only digits.
  - `{key|msisdn}` keeps only digits and rewrites a leading `62` to `0`.
- A missing key, or an empty result, makes the order go to the queue with a "template can't be applied" reason. It is never sent.
- Templates are stored per **(seller_name, itemku game)**. `seller_name` comes from the cached price list for the chosen `buyer_sku_code`. The itemku game is identified by `game_name` (or game id if available).
- The owner edits a template once per pair (group sheet on the Products screen). The UI shows a live example conversion.

### 8.3 Product mapping and modes

- Each itemku product (`product_id`) maps to an **ordered list** of Digiflazz `buyer_sku_code`s.
  - The first is the primary SKU, used by Auto.
  - The others are alternates offered in the manual retry picker.
- `mode` is `off | manual | auto`. Auto requires a linked primary SKU with a known seller and a template for its (seller, game) pair.
- **Operating rule for the owner (shown in Settings):** SKUs used in Auto must have Digiflazz backup sellers disabled. The API cannot tell us which seller actually fulfilled a transaction, so a silent fallback could receive a wrongly formatted ID.

- **Seller change:** if a price-list sync shows a different `seller_name` for a product's primary SKU, an Auto product drops to Manual and an alert is sent. The template pair changed, so Auto must be re-confirmed by the owner.

### 8.4 Auto turn-off and back on

- Price-list sync every 15 min stores every SKU's `seller_name`, `price`, status, stock, `multi` and cut-off times.
- After each sync, for each product whose mode is not `off`, check its primary SKU. The product is **unavailable** if any of these hold:
  - `buyer_product_status=false`
  - `seller_product_status=false`
  - `unlimited_stock=false` and `stock=0`
  - the current WIB time is inside the cut-off window
  - the SKU is missing from the list
  - `price ≥` the itemku price
- "No margin" means: Digiflazz `price` > `net_unit − min_margin`.
  - `net_unit` = itemku price × (1 − fee rate).
  - Fee rate per product is learned from its most recent order: `1 − order_income / (price × quantity)`. Until a product has an order, it falls back to a global "itemku fee %" in Settings, which the owner enters.
  - `min_margin` is a Settings value in Rupiah. Default 0.
- If unavailable:
  1. Remember the current itemku stock and mode.
  2. Set itemku stock to 0.
  3. Mark the product `off`, with `off_reason=auto:<reason>`.
  4. Send a Telegram alert.
- If a product is `off` with an `auto:` reason and the cause has cleared, restore the saved stock and mode and send an alert.
- A product the owner turned off by hand (`off_reason=manual`) is never turned back on automatically.
- Turning Off also stops new orders only. Orders already received are still processed normally.
- Turning back on restores the stock saved at turn-off. No sales happen while stock is 0, so that number is still correct.
- **Stock locked:** if `stock/update` returns `UPDATE_STOCK_REJECTED` (seen on "Pengiriman Otomatis" products), the product is flagged `stock_locked`.
  - The Off option is disabled for it in the UI, with the reason shown.
  - When auto-off would apply, the app instead drops it to Manual (it won't spend money on a no-margin or unavailable SKU) and sends an urgent Telegram alert: "turn this off in the itemku dashboard".

### 8.4b Delivery deadlines

- Each product has a `deadline_class`: `regular` (24 h), `ten_min` (10 min) or `instant` (2 min).
  - The owner sets it on the item's sheet; the default is `regular`.
  - A product flagged `stock_locked` is suggested as `instant`. The itemku API does not expose the delivery type, so the owner confirms it.
- An order's deadline = `order_created_at` + its class.
- The Queue sorts `needs_you` by **nearest deadline first**, not oldest. The focus card and each row show a countdown, which turns red under 25% of the window.
- **Before every purchase** (auto, manual or retry), if less than 30 s of the window remains or the window has passed, the app re-reads the order via `order/list`. If the order is no longer `REQUIRE_PROCESS`, it does not buy.
- **Fast classes (`ten_min`, `instant`) in Auto:** on `Gagal` with the primary SKU, Auto immediately tries the product's alternate SKUs in order. An alternate is skipped if it has no template for its (seller, game) pair, or if it fails the `max_price` rule. Only when all alternates fail does the order go to `needs_you`, with an urgent alert.
  - A manual retry inside 2 minutes is not realistic. This overrides the general "Auto uses only the primary SKU" rule for these classes only.
- **Cancelled by itemku:** when the poll or a re-read sees an open order as `REFUNDED`, the order becomes `cancelled`.
  - If any attempt for it is `Sukses`, `Pending` or `unknown`, send an urgent alert: money may be spent on a cancelled order.
  - Pending attempts are still tracked to the end so the owner knows the outcome.
- **Telegram for regular orders in `needs_you`:** one alert on entry, then reminders when 4 h and 1 h are left.
- **Telegram for fast classes:** every `needs_you` alert is marked urgent.
- With 2-minute orders, the itemku callback is the primary path. The backup poll runs every 30 s, not 60 s.

### 8.5 Buying (per unit), the double-buy guard

- An order with `quantity = N` needs N successful units. Each purchase attempt is a row in `attempts`:
  - `ref_id` (UNIQUE, e.g. `{order_id}-{unit}-{attempt_no}`)
  - `order_id`, `unit`, `buyer_sku_code`, `customer_no`, `status`, `rc`, `sn`, `price`, timestamps
- **Price cap:** every purchase for an itemku order sends Digiflazz `max_price` = `floor(order_income / quantity) − min_margin`. A price rise since the last sync then fails the purchase (`Gagal`) instead of losing money. On a manual retry, the owner may tick "buy at a loss" to send no cap. For direct orders, the cap = the paid price per unit if entered, else none.
- **Guard:** a new attempt for a unit may only be created if every previous attempt for that unit is `Gagal`. This check and the insert run in one DB transaction.
- The attempt row is written **before** calling Digiflazz.
  - If the HTTP call times out, the attempt stays `unknown`. The re-check loop resolves it by re-sending the same `ref_id`, which is safe.
  - A unit is never retried while it has an `unknown` or `Pending` attempt.
- If `quantity > 1` and the SKU has `multi=false`, the order goes straight to the queue.
- **Auto mode:** buy each unit with the primary SKU, sequentially.
  - Any `Gagal` puts the order in the queue with the rc and message.
  - `Pending` puts it in "Processing".
  - Resolution arrives via the Digiflazz webhook (verify `X-Hub-Signature` with constant-time compare and match `ref_id`), or via the re-check loop every 2 min that re-sends pending/unknown `ref_id`s.
- **Manual / retry:** the owner may edit `customer_no` and pick any mapped SKU. Only units without a successful or open attempt are bought. For itemku orders, the app re-reads the order via `order/list` right before buying and refuses if it is no longer `REQUIRE_PROCESS`.

### 8.6 Delivering on itemku

- When all N units are `Sukses`, call `order/action DELIVER`.
- On a timeout, non-200 or `DELIVER_ORDER_IN_PROGRESS`: do not assume. Re-read the order via `order/list` and retry until its status is `DELIVERED`.
  - `ORDER_DELIVERED` counts as success.
  - `MISSMATCH_STATUS` / `ORDER_REJECTED` (e.g. the buyer cancelled) go to the queue with an alert. Money was already spent on Digiflazz.
- **Reject & refund** (owner action only): `order/action REJECT` with a `refund_reason` the owner picks.
  - Default: `WRONG_BUYER_INFORMATION` after an rc for a wrong number, otherwise `NO_STOCK`.
  - Blocked if any unit is `Sukses` or open, unless the owner explicitly confirms.

### 8.7 Order states (internal)

`new → buying → processing (pending) → delivering → delivered`

From any of these states except `delivered`, the order can go to `needs_you`, with a reason:
- `auto_failed`
- `manual_product`
- `partial`
- `multi_blocked`
- `template_error`
- `unsupported_type`
- `deliver_failed`

From `needs_you`, the owner can choose `buying` (retry) or `rejected`.

Separate end state: `cancelled`. The order was refunded or cancelled by itemku (deadline passed or buyer cancelled), or a direct order was cancelled by the owner.

The Queue screen's "N to handle" counts `needs_you`.

### 8.8 Direct orders (WhatsApp buyers)

- `orders` carries a `source` column: `itemku` | `direct`. Direct orders get an app-generated id and use the same `attempts` table, double-buy guard, pending handling and webhook matching (§8.5).
- **Create** ("+ New order" on the Queue screen). The owner picks either:
  - a mapped itemku product. Its primary/alternate SKUs and (seller, game) template apply. The form shows one input per template placeholder and renders the converted `customer_no` live.
  - **or** any SKU from the cached Digiflazz price list. The owner types the final `customer_no` directly.
- Optional fields: buyer name, buyer WhatsApp number, price the buyer paid (for margin), quantity.
- Mode does not apply: a direct order is bought immediately on submit (the owner is the confirmation). The product's Off state does not block direct orders.
- **Failure:** the order goes to `needs_you` with a "Direct" tag. Retry works like itemku orders. Instead of "Reject & refund", the option is **"Cancel"**: no external call, just closes the order; the owner refunds the buyer outside the app.
- **Success:** when all units are `Sukses`, the order is `delivered` with no itemku call. The order shows a **"Send to WhatsApp"** button that opens `https://wa.me/<number in 62… form>?text=<urlencoded>`. The message includes product, customer number and SN per unit. If no number was entered, the button copies the text instead.
- History lists both sources with a source filter.

### 8.9 Telegram alerts

Send a Telegram alert for each of these:
- an order entered `needs_you` (one message per order, with game, product and reason)
- a product auto-turned off or back on
- Digiflazz balance below a threshold (set in Settings, checked every 10 min)
- no itemku callback received while the poll found orders (callback likely broken)
- the poll returned 30 orders (possible backlog)
- `deliver_failed`

Send via the Bot API `sendMessage` to one configured chat id.

### 8.10 Auth and security

- Single admin: password (bcrypt hash in env) → HttpOnly, Secure, SameSite=Strict session cookie. Login is rate-limited.
- HTTPS only.
- Webhook endpoints are public but verified:
  - itemku by shop-id header + `order/list` re-check
  - Digiflazz by HMAC
- Log all external API requests and responses (with secrets redacted) to an `api_log` table for debugging. Builder decides retention, e.g. 30 days.

## 9. UI (direction B)

Mockups: `docs/superpowers/specs/mockups/queue-direction-b.html` (right phone) and `products.html`.

**Feel.** Light, calm, a desk with one sheet on it. The owner is usually on their phone between other things. Each visit should answer "what needs me?" and let them act in one tap.

**Tokens (from the mockups):**

| Token | Value | Use |
|---|---|---|
| `--desk` | `#F4F4F1` | page background |
| `--sheet` | `#FFFFFF` | cards |
| `--well` | `#EDEDEA` | inputs and segmented-control tracks |
| `--line` / `--line-2` | `rgba(17,19,23,.07/.13)` | borders |
| `--hi` / `--mid` / `--lo` / `--dim` | `#111317` / `#5B606A` / `#8C9099` / `#C4C6CB` | text, four levels |
| `--sent` | `#14955A` | success |
| `--hold` | `#B9780B` | pending / manual |
| `--stop` | `#D23B2C` | failed / off |
| `--act` | `#2F54EB` | inline links like "Edit" only |

- **Type:** Geist for UI, Geist Mono for IDs, order numbers and customer numbers. Tabular numbers for all money.
- **Depth:** subtle ring + soft shadow on cards. Primary button is solid `--hi` (near black).
- **Spacing:** 4px grid. Card radius 18–20, controls 10–12. Touch targets ≥ 44 px.

**Screens:**

1. **Queue** (home)
   - Header "N to handle" plus balance and today's auto count, and a **"+ New order"** button that opens the direct-order form (§8.8) as a full-height sheet.
   - The **focus card** shows the oldest `needs_you` order:
     - reason pill
     - product
     - order number and age
     - what the buyer gave
     - the converted customer number (large, mono, editable)
     - chosen SKU / seller, cost and margin
     - the plain-language reason
     - primary **Buy & deliver**
     - secondary "Other seller ▾" (picker of mapped SKUs; the number re-renders with that seller's template)
     - **Reject & refund** (with a confirm sheet and reason picker)
   - Below it, "Up next": compact rows for the remaining `needs_you` and `processing` orders. Tapping one makes it the focus card.
   - Empty state: "Nothing needs you", with today's counts.
2. **Products**
   - Filter chips: All / Auto / Off / Unlinked.
   - Groups per (game, seller) with the template in the header.
   - Each item shows cost → itemku price and margin (red if negative), plus one segmented control **Off · Man · Auto**. Auto is disabled when the item is unlinked; an auto-off reason is shown under the item.
   - The group "•••" opens a bottom sheet: edit template with a live example, set all Auto / all Manual / turn all off.
   - Item tap opens linking: search the cached Digiflazz price list by brand/name, set primary and alternate SKUs.
3. **History**
   - Delivered, rejected and processing orders with attempts (ref_id, SKU, rc, sn, price).
   - Search by order number or customer number.
4. **Settings**
   - Balance alert threshold, Telegram test button.
   - Connection status (last callback, last poll, last price sync, last error per API).
   - The "disable backup sellers for Auto SKUs" rule.
   - Logout.

Desktop: the same screens with a wider max-width. On wide screens the queue list and the focus card may sit side by side (builder's call).

## 10. Testing

- Unit tests (Go):
  - itemku token signing against the PDF example
  - Digiflazz md5 signs and webhook HMAC check
  - template rendering and filters
  - the double-buy guard (concurrent attempts on one unit)
  - the order state machine
  - auto-off rules
- Integration:
  - Digiflazz `testing: true` and its published test cases
  - itemku sandbox (`/dev/api/order/generate`, `/dev/api/product/generate`). The sandbox has **no callback**, so the poll path is tested there and the callback path in production with one cheap real order.
- A dry-run mode flag: the full flow runs, but the Digiflazz call uses `testing: true` and the itemku DELIVER call is logged, not sent. Used for the first days in production.

## 11. Left to the builder

- VPS provider, Caddy config, systemd unit, backup of the SQLite file (e.g. a nightly copy off-box).
- Exact DB schema beyond the constraints stated (orders PK = itemku `order_id`, attempts `ref_id` UNIQUE, the one-open-attempt-per-unit guard).
- Go package layout and frontend component structure.
- Poll and loop intervals within the ranges stated.
- How the itemku game is keyed for templates (`game_name` vs a game id) once real order payloads are seen.
- Desktop layout details.

## 12. Calls made by the designer (not explicitly confirmed by the owner)

- Go stdlib + SQLite + goroutine loops, frontend embedded in the binary, Caddy for TLS.
- SKU-to-seller resolution via the price list, plus the "no backup sellers on Auto SKUs" operating rule.
- Quantity > 1 handled as N sequential purchases; `multi=false` with quantity > 1 goes to the queue.
- Auto mode uses only the primary SKU; alternates are used only on manual retry.
- Auto turn-back-on only for app-initiated Off; manual Off is sticky.
- Net income per unit learned from each product's latest `order_income`, with a Settings fallback fee % and a `min_margin` setting (default Rp 0); same value used as Digiflazz `max_price`.
- `stock_locked` products: Off is disabled; auto-off drops them to Manual + urgent alert instead.
- Deadline class set by owner per product (API doesn't expose it); 30 s pre-buy status re-read; queue sorted by deadline.
- **Fast-class Auto tries alternate SKUs before the queue.** This overrides the owner's earlier "failures go to the manual queue" answer, but only for 2-min and 10-min products, where a manual retry is unrealistic.
- Reminder schedule for regular orders: on entry, 4 h left, 1 h left.
- Backup poll every 30 s.
- Template filters `|digits` and `|msisdn`.
- Single admin login; dry-run mode for rollout.
- Seller change on a primary SKU drops Auto to Manual; itemku order status re-checked before every manual buy.
- Direct orders: bought on submit, ignore product Off state, "Cancel" instead of refund, WhatsApp hand-off via `wa.me` link.
