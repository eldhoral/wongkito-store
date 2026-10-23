# itemku ⇄ Digiflazz Relay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A phone-first web app that receives itemku orders (and manual WhatsApp orders), buys the top-up on Digiflazz with the right per-seller customer-number format, and closes the itemku order, with per-product Off / Manual / Auto.

**Architecture:** One Go binary (`api/`) holds the HTTP server, webhooks, SQLite store and goroutine loops. It embeds the built React PWA (`web/dist`). Caddy terminates TLS on a fixed-IP VPS. All money-safety rules live in `internal/relay` and are tested against fake itemku/Digiflazz clients.

**Tech Stack:**
- Go 1.23, stdlib `net/http`, `modernc.org/sqlite`, `golang.org/x/crypto/bcrypt`
- React 18 + Vite + TypeScript + Tailwind v4, `@tanstack/react-query`, `vaul` (bottom sheets), Geist / Geist Mono fonts
- Vitest for the frontend

**Spec:** `docs/superpowers/specs/2026-10-23-itemku-digiflazz-relay-design.md` (read it first; section numbers below refer to it). Mockups: `docs/superpowers/specs/mockups/queue-direction-b.html` (right phone) and `products.html`.

## Global Constraints

- **Monorepo:** `api/` (Go module `wongkito/api`), `web/`, `docs/`, `deploy/`, root `Makefile`. No Turborepo or Nx, no PHP, no Redis, no job library, no Go web framework.
- **Secrets come only from env vars and never reach the browser:** `ITEMKU_API_KEY`, `ITEMKU_SECRET`, `ITEMKU_SHOP_ID`, `ITEMKU_BASE_URL`, `DF_USERNAME`, `DF_API_KEY`, `DF_WEBHOOK_SECRET`, `TG_BOT_TOKEN`, `TG_CHAT_ID`, `ADMIN_PASSWORD_HASH` (bcrypt), `SESSION_SECRET`, `DB_PATH`, `LISTEN_ADDR`, `DRY_RUN`.
- **itemku auth:** header JSON `{"X-Api-Key":…,"Nonce":…,"alg":"HS256"}` and payload = the exact request body bytes. Each is base64url-encoded without padding, joined by `.`, then signed with HMAC-SHA256 using the secret. The signature is base64url without padding. HTTP headers: `X-Api-Key`, `Nonce`, `Authorization: Bearer <token>`, `Content-Type: application/json`.
- **Digiflazz sign strings:**
  - `md5(username+apiKey+ref_id)` for transactions
  - `md5(username+apiKey+"pricelist")` for the price list
  - balance: confirm the exact sign string from the Digiflazz "Cek Saldo" docs before coding
  - webhook: `X-Hub-Signature: sha1=<hex HMAC-SHA1(raw body)>`, compared with `hmac.Equal`
- **Deadline classes:** `regular` = 24 h, `ten_min` = 10 min, `instant` = 2 min.
- **Intervals:**
  - backup order poll: 30 s
  - pending re-check: 2 min
  - price-list sync: 15 min
  - balance check: 10 min
  - the pre-buy itemku re-read triggers when ≤ 30 s of the window are left
- **itemku refund reasons are used verbatim:** `NO_STOCK`, `WRONG_BUYER_INFORMATION`, `SERVER_ERROR`, `PRICE_CHANGED`, `BUYER_UNREACHABLE`.
- **Money:** whole Rupiah stored as `int64`. Display uses `id-ID` grouping (`Rp 1.284.500`) and tabular numerals.
- **Time:** all deadline and cut-off math is in WIB (`Asia/Jakarta`). Store UTC unix seconds.
- **UI tokens** are exactly those in spec §9 (`--desk`, `--sheet`, `--well`, `--line`, `--line-2`, `--hi`, `--mid`, `--lo`, `--dim`, `--sent`, `--hold`, `--stop`, `--act`). Touch targets are at least 44 px. Grid is 4 px.

## Review Focus

1. **Digiflazz numbers as strings.** The docs show `price` and `stock` as both string and number. Decoding must accept `"44210"` and `44210`. Pinned in Task 4.
2. **Duplicate or concurrent intake.** The callback and the poll can deliver the same order at the same moment. There must be exactly one order row and one purchase. Pinned in Tasks 6 and 7.
3. **Buyer input with stray whitespace, `+62`, dashes, or a missing `zone_id`.** Must render correctly or route to `template_error`, and never be sent half-filled. Pinned in Tasks 2 and 6.
4. **itemku DELIVER times out but actually succeeded.** Must end `delivered` with no second buy and no false failure alert. Pinned in Task 8.
5. **Webhook for an unknown `ref_id`, or a replayed webhook after the order is delivered.** Must be ignored without a state change or error loop. Pinned in Task 8.

---

### Task 1: Repo scaffold, config, store and migrations

**Files:**
- Create: `Makefile`, `api/go.mod`, `api/internal/config/config.go`, `api/internal/store/db.go`, `api/internal/store/migrations/001_init.sql`, `api/internal/store/db_test.go`

**Interfaces:**
- Produces:
  - `config.Load() (config.Config, error)`: one field per env var in Global Constraints. `DryRun bool`. `ItemkuBaseURL` defaults to `https://tokoku-gateway.itemku.com`.
  - `store.Open(path string) (*store.Store, error)`: WAL mode, `busy_timeout=5000`, runs embedded migrations in filename order, tracked in `schema_migrations`.
  - `(*store.Store).DB *sql.DB`.

Schema in `001_init.sql`:
- `orders`
  - `id TEXT PK` (`"ik-<order_id>"` or `"d-<unix_nano>"`)
  - `source TEXT CHECK(source IN ('itemku','direct'))`
  - `itemku_order_id INTEGER UNIQUE`
  - `order_number`, `product_id INTEGER`, `sku_override TEXT`, `game_name`, `product_name`
  - `price`, `quantity`, `order_income`
  - `required_info TEXT` (JSON object), `customer_no`
  - `buyer_name`, `buyer_wa`, `paid_price`
  - `state`, `reason`
  - `deadline_at`, `created_at`, `updated_at`, `delivered_at`
- `attempts`
  - `ref_id TEXT PK`, `order_id`, `unit`, `attempt_no`
  - `buyer_sku_code`, `customer_no`, `max_price`
  - `status` (`unknown`|`Pending`|`Sukses`|`Gagal`), `rc`, `message`, `sn`, `price`
  - `created_at`, `updated_at`
- **Double-buy guard as a DB constraint:** `CREATE UNIQUE INDEX attempts_one_open ON attempts(order_id, unit) WHERE status <> 'Gagal';`
- `products`
  - `id INTEGER PK` (itemku product id), `name`, `game_name`, `item_type`
  - `itemku_price`, `itemku_stock`
  - `mode` (`off`|`manual`|`auto`), `off_reason`, `saved_stock`, `saved_mode`
  - `deadline_class` default `'regular'`, `stock_locked INTEGER` default 0
  - `fee_rate REAL`, `last_seller`
- `product_skus(product_id, position, buyer_sku_code, PK(product_id, position))`
- `pricelist`
  - `buyer_sku_code PK`, `product_name`, `category`, `brand`, `type`, `seller_name`, `price`
  - `buyer_product_status`, `seller_product_status`, `unlimited_stock`, `stock`, `multi`
  - `start_cut_off`, `end_cut_off`, `synced_at`
- `templates(seller_name, game_name, template, PK(seller_name, game_name))`
- `settings(key PK, value)`
- `api_log(id INTEGER PK, at, service, endpoint, req, resp, http_status, ms)`
- `alerts_sent(key PK, at)`

- [ ] **Step 1: Write failing tests** in `db_test.go`:
  - `TestOpenMigratesTwice`: `Open` on a temp path twice succeeds.
  - `TestOneOpenAttemptPerUnit`:
    - insert attempt (o1, unit 0, `Pending`)
    - a second (o1, 0, `unknown`) fails with a UNIQUE error
    - after updating the first to `Gagal`, the second insert succeeds
    - an (o1, 0, `Gagal`) row can always be added
  - `TestItemkuOrderUnique`: two `orders` rows with the same `itemku_order_id` fail.
- [ ] **Step 2: Run** `cd api && go test ./internal/store/...`. Expected: FAIL (package missing).
- [ ] **Step 3: Implement** `config.Load` and `store.Open`. Migrations are embedded via `//go:embed migrations/*.sql`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Root `Makefile`.** Targets:
  - `dev-api` (`go run ./cmd/server`)
  - `dev-web` (`npm --prefix web run dev`)
  - `test` (`cd api && go test ./...` and `npm --prefix web test -- --run`)
  - `build` (web build → copy `web/dist` to `api/internal/web/dist` → `go build -o bin/wongkito ./cmd/server`)
- [ ] **Step 6: Commit** `feat(api): store, schema, double-buy guard index`.

### Task 2: Customer-number templates

**Files:** Create `api/internal/tmpl/tmpl.go`, `api/internal/tmpl/tmpl_test.go`

**Interfaces:**
- Produces:
  - `tmpl.Render(template string, info map[string]string) (string, error)`
  - `tmpl.Keys(template string) []string`: placeholder names without filters, in order, de-duplicated.
  - Errors: `tmpl.ErrMissingKey` (wrapped with the key name) and `tmpl.ErrEmpty`.

- [ ] **Step 1: Failing table test `TestRender`:**

| Template | Info | Result |
|---|---|---|
| `{player_id}\|{zone_id}` | `player_id:" 12345678 "`, `zone_id:"1234"` | `"12345678\|1234"` |
| `{player_id}{zone_id}` | same | `"123456781234"` |
| `{phone\|digits}` | `"0812-3456-7890"` | `"081234567890"` |
| `{phone\|msisdn}` | `"+62 812 3456 7890"` | `"081234567890"` |
| `{phone\|msisdn}` | `"081234567890"` | unchanged |
| `{player_id}\|{zone_id}` | no `zone_id` | `errors.Is(err, ErrMissingKey)` |
| `{x\|digits}` | `x:"abc"` | `ErrEmpty` |
| unknown filter `{x\|upper}` | | error |

  Also `TestKeys`: `Keys("{a}|{b|digits}{a}") == []string{"a","b"}`.
- [ ] **Step 2: Run** `go test ./internal/tmpl/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Use a single regexp `\{([a-zA-Z0-9_ ]+)(\|(digits|msisdn))?\}`. Literal text outside placeholders is kept as-is. Values are `strings.TrimSpace`d.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): customer-number template renderer`.

### Task 3: itemku client

**Files:** Create `api/internal/itemku/client.go`, `api/internal/itemku/types.go`, `api/internal/itemku/client_test.go`

**Interfaces:**
- Produces:
  - `itemku.Token(apiKey, secret, nonce string, body []byte) string`
  - `type Order struct`, with JSON fields from spec §5.1:
    - `OrderID int64 \`json:"order_id"\``, `OrderNumber`, `ProductID int64`, `Price`, `Quantity`, `OrderIncome int64`
    - `GameName`, `ProductName`, `Status`
    - `RequiredInformation string`, `UsingDeliveryInfo bool`
    - `OrderCreatedEpoch int64`, `BuyerName`, `CategoryName`
  - `(Order).Info() (map[string]string, error)`: parses the JSON-string `required_information`. Number values become strings.
  - `type Product struct{ID int64; Name, GameName, ItemTypeName string; Price, Stock int64}`
  - `type APIError struct{HTTP int; Code, Message string}`, implements `error`
  - `var ErrStockLocked`: returned when `statusCode == "UPDATE_STOCK_REJECTED"`.
  - `type Client struct{BaseURL, APIKey, Secret string; HTTP *http.Client; Now func() time.Time; Log func(endpoint string, req, resp []byte, status int, d time.Duration)}`
  - Methods:
    - `ListOpen(ctx, since time.Time, limit int) ([]Order, error)`: `order_status=REQUIRE_PROCESS`
    - `GetOrder(ctx, orderID int64) (*Order, error)`: `date_start` = now − 59 days. Returns `nil, nil` if not found.
    - `Deliver(ctx, orderID int64) error`
    - `Reject(ctx, orderID int64, reason string) error`
    - `ListProducts(ctx, page int) (items []Product, next int, err error)`
    - `UpdateStock(ctx, productID, stock int64) error`

- [ ] **Step 1: Failing tests:**
  - `TestTokenMatchesKnownVector`:
    - `Token("YourApiKeyProvidedByItemku","YourApiSecretProvidedByItemku","1707969441",[]byte(`{"order_id":1234,"date_start_at":"2023-11-01"}`))`
    - equals `eyJYLUFwaS1LZXkiOiJZb3VyQXBpS2V5UHJvdmlkZWRCeUl0ZW1rdSIsIk5vbmNlIjoiMTcwNzk2OTQ0MSIsImFsZyI6IkhTMjU2In0.eyJvcmRlcl9pZCI6MTIzNCwiZGF0ZV9zdGFydF9hdCI6IjIwMjMtMTEtMDEifQ.esZb-pATbYL51BPJf-NP7UvLj46KwauABSRtEQkhyLI`
  - `TestRequestIsSignedOverExactBody`: an `httptest.Server` recomputes `Token` from the received headers and body, and the result equals the `Authorization` bearer.
  - `TestUpdateStockLocked`: the server answers `{"success":false,"statusCode":"UPDATE_STOCK_REJECTED"}`, so `errors.Is(err, ErrStockLocked)`.
  - `TestOrderInfo`: `required_information` `"{\"player_id\":\"123\",\"zone_id\":4567}"` gives `map{player_id:"123", zone_id:"4567"}`.
- [ ] **Step 2: Run** `go test ./internal/itemku/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - One private `do(ctx, path string, body any, out any) error`. It marshals the body once (compact JSON) and signs those bytes.
  - It treats `success:false` as `*APIError`, and calls `Log` with secrets absent.
  - Default HTTP timeout: 15 s.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): itemku tokoku client`.

### Task 4: Digiflazz client and webhook verification

**Files:** Create `api/internal/digiflazz/client.go`, `api/internal/digiflazz/client_test.go`

**Interfaces:**
- Produces:
  - `digiflazz.Sign(username, key, suffix string) string` (md5 hex)
  - `type Num int64`: `UnmarshalJSON` accepts a JSON number or a numeric string, and accepts `""` as 0.
  - `type TxResult struct{RefID, CustomerNo, SKU, Message, Status, RC, SN string; Price Num; BuyerLastSaldo float64}`: JSON tags `ref_id`, `customer_no`, `buyer_sku_code`, `message`, `status`, `rc`, `sn`, `price`, `buyer_last_saldo`.
  - `type TopupReq struct{SKU, CustomerNo, RefID string; MaxPrice int64; Testing bool}`: `MaxPrice` 0 means omitted.
  - `type PriceItem struct`: all §5.2 price-list fields. `Price` and `Stock` are `Num`. Bools stay bools. `StartCutOff`, `EndCutOff` are strings.
  - `type Client struct{Username, APIKey, BaseURL string; HTTP *http.Client; Log func(...)}` (same `Log` signature as Task 3)
  - Methods:
    - `Topup(ctx, TopupReq) (TxResult, error)`: also used for status re-check by re-sending the same `RefID`
    - `PriceList(ctx) ([]PriceItem, error)`
    - `Balance(ctx) (int64, error)`
  - `digiflazz.VerifyWebhook(secret string, body []byte, sigHeader string) bool`
  - `digiflazz.ParseWebhook(body []byte) (TxResult, error)`: reads `{"data":{…}}`

- [ ] **Step 1: Failing tests:**
  - `TestSign`: `Sign("user","key123","ref-1") == "e88d9302efc81d70626daabbd2bfb5d0"` and `Sign("user","key123","pricelist") == "c10f1cd8a9dd682fd26628048b489ec6"`
  - `TestVerifyWebhook`:
    - secret `whsecret`, body `{"data":{"ref_id":"r1","status":"Sukses"}}`, header `sha1=9a030568429a73918423bfa7e6e2a1ae33064ba4` → true
    - a one-char-altered body → false
    - header without the `sha1=` prefix → false
  - `TestNumAcceptsStringAndNumber`: `{"price":"44210","stock":7}` → `44210`, `7`
  - `TestTopupBody`: an httptest server asserts the JSON has `username`, `buyer_sku_code`, `customer_no`, `ref_id`, `sign`, `max_price` (present only when > 0) and `testing` (only when true)
- [ ] **Step 2: Run** `go test ./internal/digiflazz/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Base URL is `https://api.digiflazz.com/v1`. Responses are wrapped in `data`. HTTP timeout: 30 s. A transport error returns `err` with an empty `TxResult`; the caller treats it as `unknown`.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): digiflazz client and webhook verify`.

### Task 5: Telegram notifier

**Files:** Create `api/internal/telegram/telegram.go`, `api/internal/telegram/telegram_test.go`

**Interfaces:**
- Produces:
  - `type Bot struct{Token, ChatID, BaseURL string; HTTP *http.Client}`
  - `(*Bot).Send(ctx, text string, urgent bool) error`: urgent messages are prefixed `🚨 `. Uses `sendMessage` with `parse_mode` unset.
  - `type Notifier interface{ Send(ctx context.Context, text string, urgent bool) error }`, used by relay for fakes.

- [ ] **Step 1: Failing test** `TestSendPostsToChat`: an httptest server receives `POST /bot<TOKEN>/sendMessage` with `chat_id` and `text` starting `🚨 ` when urgent.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): telegram notifier`.

### Task 6: Relay engine, intake and routing

**Files:** Create `api/internal/relay/engine.go`, `api/internal/relay/intake.go`, `api/internal/relay/fakes_test.go`, `api/internal/relay/intake_test.go`

**Interfaces:**
- Consumes: `store.Store` (T1), `tmpl.Render` (T2), `itemku.Order`/`Product` (T3), `digiflazz.TxResult`/`TopupReq`/`PriceItem` (T4), `telegram.Notifier` (T5).
- Produces:
  - `type Itemku interface{ GetOrder; ListOpen; Deliver; Reject; ListProducts; UpdateStock }`, with the Task 3 signatures
  - `type Digiflazz interface{ Topup; PriceList; Balance }`, with the Task 4 signatures
  - `type Engine struct{DB *store.Store; IK Itemku; DF Digiflazz; TG telegram.Notifier; Now func() time.Time; DryRun bool; mu sync.Mutex}`
    - `mu` is held around each order's state transitions
    - ponytail: a global lock is fine at 100 orders/day; use per-order locks if throughput matters
  - Order states (const strings): `new`, `buying`, `processing`, `delivering`, `delivered`, `needs_you`, `rejected`, `cancelled`
  - Reasons: `auto_failed`, `manual_product`, `partial`, `multi_blocked`, `template_error`, `unsupported_type`, `deliver_failed`, `no_mapping`
  - `(*Engine).Intake(ctx, o itemku.Order) (created bool, err error)`
    - `INSERT … ON CONFLICT(itemku_order_id) DO NOTHING`
    - `deadline_at = OrderCreatedEpoch + class` (product's `deadline_class`, default `regular`)
    - then calls `Route`
  - `(*Engine).VerifyAndIntake(ctx, orderID int64) error`: used by the callback. Runs `GetOrder` and intakes only if `Status == "REQUIRE_PROCESS"`.
  - `(*Engine).Route(ctx, id string) error`, routing rules in order:
    1. `UsingDeliveryInfo` → `needs_you/unsupported_type`
    2. product unknown or no SKUs → `needs_you/no_mapping`
    3. primary SKU's template missing, or `Render` error → `needs_you/template_error`
    4. `quantity>1` and SKU `multi=false` → `needs_you/multi_blocked`
    5. mode `manual` or `off` → `needs_you/manual_product`
    6. otherwise `BuyAuto` (Task 7)
  - `(*Engine) toQueue(ctx, id, reason, detail string)`: sets the state and sends a Telegram alert (urgent if the deadline class is `ten_min`/`instant`). The message includes game, product, order number and reason.
  - Helpers used later:
    - `(*Engine) primaryTemplate(ctx, productID int64, sku string) (tpl, seller string, err error)`: template lookup by (`pricelist.seller_name`, `products.game_name`)

`fakes_test.go`: `fakeIK` and `fakeDF` hold in-memory maps, with call counters and scriptable responses. `newTestEngine(t)` gives a temp DB, the fakes, a fake notifier collecting messages, and a fixed `Now`.

- [ ] **Step 1: Failing tests** in `intake_test.go`:
  - `TestIntakeIdempotent`: `Intake` the same order twice, then 20 concurrent goroutines → one row; `created` is true exactly once.
  - `TestVerifyAndIntakeSkipsNonOpen`: `fakeIK.GetOrder` returns `DELIVERED` → no row.
  - `TestRouteManual`: product mode `manual` → state `needs_you`, reason `manual_product`, one Telegram message.
  - `TestRouteTemplateErrorMissingZone`: auto product, template `{player_id}|{zone_id}`, info lacks `zone_id` → `needs_you/template_error`, `fakeDF.Topup` calls == 0.
  - `TestRouteMultiBlocked`: qty 2, SKU `multi=false` → `needs_you/multi_blocked`.
  - `TestDeadlineFromClass`: `instant` product → `deadline_at == created + 120`.
- [ ] **Step 2: Run** `go test ./internal/relay/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Use a stub `BuyAuto` returning nil until Task 7.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): idempotent intake and routing`.

### Task 7: Relay buying (guard, price cap, fast-class alternates, pre-buy check)

**Files:** Create `api/internal/relay/buy.go`, `api/internal/relay/buy_test.go`

**Interfaces:**
- Produces:
  - `type BuyOpts struct{SKU, CustomerNo string; NoCap bool}`: empty SKU means primary; empty CustomerNo means rendered from that SKU's template.
  - `(*Engine).BuyAuto(ctx, id string) error`: auto path (§8.5, §8.4b)
  - `(*Engine).BuyManual(ctx, id string, o BuyOpts) error`: owner retry. For itemku orders it always re-reads `GetOrder` first (spec §8.5).
  - `(*Engine) buyUnits(ctx, id string, sku, customerNo string, maxPrice int64) (allSukses bool, err error)`: shared core
  - `(*Engine) maxPrice(o order) int64`:
    - itemku: `order_income/quantity − min_margin` (setting `min_margin`, default 0)
    - direct: `paid_price/quantity`, or 0
  - `(*Engine) preBuyCheck(ctx, o order) (ok bool, err error)`: if `deadline_at − now ≤ 30s`, re-read the order. A non-`REQUIRE_PROCESS` order → `cancelled` + returns false.

`buyUnits` algorithm (the rules the tests pin):
- For each unit `0..quantity-1` with no `Sukses`, `Pending` or `unknown` attempt:
  1. Insert the attempt row first: `ref_id = <order id>-<unit>-<attempt_no>`, status `unknown`. A UNIQUE violation means another attempt is open, so skip the unit.
  2. Call `Topup` (`Testing: e.DryRun`).
  3. Update the row from the result. On a transport error, leave it `unknown`.
  4. Stop at the first `Gagal`.
- Order state afterwards:
  - all units `Sukses` → `Deliver` (Task 8)
  - any `Pending`/`unknown` → `processing`
  - any `Gagal` with some `Sukses` → `needs_you/partial`
  - otherwise → `needs_you/auto_failed` (the detail carries rc + message + seller)
- **Fast classes** (`ten_min`, `instant`): on `Gagal` in `BuyAuto`, try each alternate SKU in `product_skus` order. Skip an alternate with no template for its seller, or whose `pricelist.price > maxPrice`. Queue only after all alternates fail.

- [ ] **Step 1: Failing tests:**
  - `TestAutoSuccessDelivers`: `fakeDF` returns `Sukses` → 1 attempt, `fakeIK.Deliver` called once, state `delivered`.
  - `TestNoSecondBuyWhilePending`: first `Topup` → `Pending`; call `BuyManual` → `Topup` count stays 1.
  - `TestTransportErrorLeavesUnknownAndBlocks`: `Topup` returns an error → attempt `unknown`, state `processing`; `BuyManual` → no new `Topup`.
  - `TestConcurrentBuyOneTopup`: 10 goroutines call `BuyManual` on the same order → `Topup` count == 1.
  - `TestMaxPriceFromIncome`: `order_income 90000`, qty 2, `min_margin 200` → `TopupReq.MaxPrice == 44800`. `NoCap` → 0.
  - `TestQuantityTwoPartial`: unit 0 `Sukses`, unit 1 `Gagal` → `needs_you/partial`. A retry buys unit 1 only (`Topup` sees `ref_id` suffix `-1-2`).
  - `TestFastClassTriesAlternates`: `instant` product, primary `Gagal`, alternate 1 has no template (skipped), alternate 2 `Sukses` → delivered, no Telegram queue alert.
  - `TestRegularDoesNotTryAlternates`: same setup with `regular` → `needs_you/auto_failed` after 1 `Topup`.
  - `TestPreBuyCheckCancelled`: `deadline_at = now+10s`, `fakeIK.GetOrder` → `REFUNDED` → state `cancelled`, `Topup` count 0.
  - `TestManualRetryChecksItemkuFirst`: order is `REQUIRE_PROCESS` in DB but `fakeIK` says `DELIVERED` → no `Topup`, error returned.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** `Route` now calls `BuyAuto`.
- [ ] **Step 4: Run** `go test -race ./internal/relay/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): guarded purchasing with price cap and alternates`.

### Task 8: Relay results, delivery, reject and cancel detection

**Files:** Create `api/internal/relay/deliver.go`, `api/internal/relay/deliver_test.go`

**Interfaces:**
- Produces:
  - `(*Engine).ApplyTx(ctx, r digiflazz.TxResult) error`
    - finds the attempt by `ref_id`; unknown `ref_id` → returns nil and does nothing
    - updates only if the status changed and the attempt is not already final
    - then recomputes the order state using the same rule as Task 7
  - `(*Engine).RecheckPending(ctx) error`: re-sends `Topup` with the same `RefID`/SKU/customer for every `Pending`/`unknown` attempt older than 60 s, then `ApplyTx`
  - `(*Engine).Deliver(ctx, id string) error`:
    - itemku: `IK.Deliver`. On any error, re-read `GetOrder`:
      - `DELIVERED` → success
      - `REQUIRE_PROCESS` → stay `delivering` (retried by `RecheckPending`'s pass over `delivering` orders)
      - `REFUNDED` → `needs_you/deliver_failed` + urgent alert
    - `ORDER_DELIVERED` error code → success
    - direct: just `delivered`
    - `DryRun`: log instead of calling `IK.Deliver`, then `delivered`
  - `(*Engine).Reject(ctx, id, reason string, force bool) error`: refuses if any attempt is `Sukses`/`Pending`/`unknown`, unless `force`. On success, `rejected`.
  - `(*Engine).Cancel(ctx, id string) error`: direct orders only, → `cancelled`
  - `(*Engine).MarkCancelledIfRefunded(ctx, o itemku.Order) error`: used by the poll. If the DB state is not final and itemku says `REFUNDED`, set `cancelled`. If any attempt is `Sukses`/`Pending`/`unknown`, send an urgent alert: "money may be spent on cancelled order".

- [ ] **Step 1: Failing tests:**
  - `TestWebhookSuksesDelivers`: attempt `Pending` → `ApplyTx{Status:"Sukses",SN:"SN1"}` → delivered, `sn` stored.
  - `TestApplyTxUnknownRefIgnored`: returns nil, no state change.
  - `TestApplyTxReplayAfterDelivered`: `ApplyTx` `Sukses` twice → `Deliver` called once.
  - `TestDeliverTimeoutButDelivered`: `fakeIK.Deliver` returns a timeout error, `GetOrder` → `DELIVERED` → state `delivered`, no alert.
  - `TestDeliverTimeoutStillOpenRetries`: `GetOrder` → `REQUIRE_PROCESS` → `delivering`. The next `RecheckPending` calls `Deliver` again.
  - `TestRejectBlockedWhenSukses`: one `Sukses` attempt → error. With `force` → `fakeIK.Reject` called with the reason.
  - `TestRefundedWithPendingAlerts`: attempt `Pending` + `MarkCancelledIfRefunded(REFUNDED)` → `cancelled`, 1 urgent message.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/relay/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): tx results, safe delivery, reject/cancel`.

### Task 9: Price-list sync, product sync and auto Off / On

**Files:** Create `api/internal/relay/catalog.go`, `api/internal/relay/catalog_test.go`

**Interfaces:**
- Produces:
  - `(*Engine).SyncProducts(ctx) error`: pages `ListProducts`, upserts `products` (name, game, type, price, stock). Never touches `mode`.
  - `(*Engine).SyncPriceList(ctx) error`: replaces `pricelist` rows, then calls `evaluateProducts`.
  - `(*Engine).SetMode(ctx, productID int64, mode string) error`: owner action
    - `off` → `UpdateStock(0)`, save `saved_stock`/`saved_mode`, `off_reason='manual'`
    - from `off` → `UpdateStock(saved_stock)`
    - `ErrStockLocked` → set `stock_locked=1`, return a typed error the UI shows
    - `auto` requires a primary SKU and a template
  - `(*Engine) evaluateProducts(ctx) error`: for each product not manually off, check the primary SKU. **Unavailable** if any of:
    - missing from the price list
    - `!buyer_product_status`
    - `!seller_product_status`
    - `!unlimited_stock && stock==0`
    - now (WIB) inside `[start_cut_off, end_cut_off)`, which may wrap midnight
    - `price > netUnit − min_margin`
  - `(*Engine) netUnit(p product) int64`: `itemku_price × (1 − fee_rate)`. `fee_rate` falls back to setting `fee_pct/100`.
  - `(*Engine) LearnFee(ctx, o itemku.Order)`: `fee_rate = 1 − order_income/(price×quantity)`. Called in `Intake`.

Actions (spec §8.3, §8.4):
- unavailable and mode ≠ off:
  - stock-locked → mode `manual` + urgent alert
  - otherwise → save stock/mode, `UpdateStock(0)`, `off_reason='auto:<reason>'`, alert
- `off_reason` starting `auto:` and now available → restore stock + mode, clear the reason, alert
- `last_seller` ≠ the current `seller_name` and mode `auto` → mode `manual` + alert. Always update `last_seller`.

- [ ] **Step 1: Failing tests:**
  - `TestAutoOffWhenNoMargin`: price 23000, fee 0.05 → net 21850; SKU price 22000 → `UpdateStock(id,0)`, `off_reason` `auto:no_margin`, `saved_stock` = prior stock, 1 alert.
  - `TestAutoBackOnRestoresStock`: next sync with price 21000 → `UpdateStock(id, saved)`, mode restored.
  - `TestManualOffStaysOff`: `SetMode(off)` then an available sync → no `UpdateStock` call.
  - `TestCutOffWrapsMidnight`: cut-off `23:00`–`01:00`, now 00:30 WIB → unavailable; 01:00 → available.
  - `TestStockLockedDropsToManual`: `UpdateStock` → `ErrStockLocked` on `SetMode(off)` → `stock_locked=1`, typed error. An unavailable sync → mode `manual`, urgent alert, no further `UpdateStock`.
  - `TestSellerChangeDropsAuto`: `last_seller` A, sync shows B → mode `manual`, alert.
  - `TestLearnFee`: price 10000, qty 2, income 18000 → `fee_rate` 0.1.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): catalog sync and auto off/on`.

### Task 10: Direct (WhatsApp) orders

**Files:** Create `api/internal/relay/direct.go`, `api/internal/relay/direct_test.go`

**Interfaces:**
- Produces:
  - `type DirectReq struct{ProductID int64; SKU string; Fields map[string]string; CustomerNo string; Quantity int64; BuyerName, BuyerWA string; PaidPrice int64}`
    - either `ProductID` (template from fields) or `SKU` + `CustomerNo`
  - `(*Engine).CreateDirect(ctx, r DirectReq) (id string, err error)`: inserts `source='direct'`, `deadline_at` NULL, then `buyUnits` immediately. Ignores product mode/Off.
  - `relay.WhatsAppLink(buyerWA, text string) string`:
    - normalises `08…`/`+62…`/`62…` to `62…`
    - returns `https://wa.me/<num>?text=<url.QueryEscape(text)>`, or `""` if no number
  - `relay.DeliveryText(o OrderView) string`: lines `Product`, `ID: <customer_no>`, `SN: <sn>` per unit

- [ ] **Step 1: Failing tests:**
  - `TestDirectFromProductRendersTemplate`: fields `player_id`/`zone_id` → `Topup.CustomerNo` `12345678|1234`, state `delivered`, `fakeIK.Deliver` count 0.
  - `TestDirectIgnoresOff`: product mode `off` → still bought.
  - `TestDirectFailCancel`: `Gagal` → `needs_you`. `Cancel` → `cancelled`, no itemku call.
  - `TestWhatsAppLink`: `"0812-3456-7890"` → prefix `https://wa.me/6281234567890?text=`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): direct whatsapp orders`.

### Task 11: Loops, reminders and health alerts

**Files:** Create `api/internal/relay/loops.go`, `api/internal/relay/loops_test.go`

**Interfaces:**
- Produces:
  - `(*Engine).Run(ctx)`: starts tickers. Each tick runs in its own goroutine with `recover` + log.
    - `PollOrders` 30 s
    - `RecheckPending` 2 min
    - `SyncProducts`+`SyncPriceList` 15 min
    - `CheckBalance` 10 min
    - `Reminders` 1 min
  - `(*Engine).PollOrders(ctx) error`:
    - `ListOpen(now−48h, 30)` → `Intake` each
    - for DB orders not final with `source='itemku'` and `deadline_at < now`: `GetOrder` → `MarkCancelledIfRefunded`
    - 30 results → alert `poll_backlog` (deduped hourly)
    - an order created via poll while the last callback is > 10 min older than that order → alert `callback_silent` (deduped hourly)
  - `(*Engine).NoteCallback()`: stores `last_callback_at`
  - `(*Engine).CheckBalance(ctx) error`: `Balance()` < setting `balance_alert` → alert (deduped per 6 h)
  - `(*Engine).Reminders(ctx) error`: `regular` orders in `needs_you` → alerts at 4 h and 1 h before `deadline_at`. Dedupe key `remind:<id>:4h|1h` in `alerts_sent`.
  - `(*Engine) alertOnce(ctx, key string, every time.Duration, text string, urgent bool)`

- [ ] **Step 1: Failing tests:**
  - `TestPollIntakesMissing`: `fakeIK.ListOpen` returns 2 orders → 2 rows.
  - `TestReminderOnceEach`: `Now` at deadline−3h59m → 1 alert; run again → still 1; at deadline−59m → 2.
  - `TestBalanceAlertDeduped`: balance 50000 < threshold 100000 → 1 alert across 3 calls.
  - `TestCallbackSilent`: `last_callback_at` = 20 min ago, poll finds a new order → 1 `callback_silent` alert.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(relay): background loops and alerts`.

### Task 12: HTTP auth and webhooks

**Files:** Create `api/internal/web/server.go`, `api/internal/web/auth.go`, `api/internal/web/hooks.go`, `api/internal/web/hooks_test.go`, `api/internal/web/auth_test.go`

**Interfaces:**
- Consumes: `relay.Engine` methods `VerifyAndIntake`, `NoteCallback`, `ApplyTx`; `digiflazz.VerifyWebhook`, `ParseWebhook`; `config.Config`.
- Produces:
  - `web.New(cfg config.Config, e *relay.Engine, static fs.FS) http.Handler`
  - Routes (Go 1.22 patterns): `POST /hooks/itemku`, `POST /hooks/digiflazz`, `POST /api/login`, `POST /api/logout`. All other `/api/` routes go through `requireAuth`. `/` serves `static` with SPA fallback to `index.html`.
  - Session: cookie `s` = `base64(expiry) + "." + hex(HMAC-SHA256(SESSION_SECRET, expiry))`. 30-day expiry. `HttpOnly; Secure; SameSite=Strict; Path=/`.
  - Login rate limit: 5 failures per 15 min per IP → 429 (in-memory map).
  - `POST /hooks/itemku`:
    - `X-itemku` ≠ `ITEMKU_SHOP_ID` → 403
    - otherwise respond `200` immediately, then in a goroutine (with a detached context) call `NoteCallback` and `VerifyAndIntake` for each `data[].order_id`
  - `POST /hooks/digiflazz`: read the raw body (1 MB cap); a bad signature → 401; otherwise `ApplyTx`, 200.

- [ ] **Step 1: Failing tests:**
  - `TestItemkuHookWrongShop403`
  - `TestItemkuHookAcks200Fast`: the handler returns before a fake `VerifyAndIntake` that blocks 1 s finishes
  - `TestDigiflazzHookBadSig401`
  - `TestDigiflazzHookAppliesTx`
  - `TestApiRequiresSession` (401 without cookie)
  - `TestLoginSetsCookieAndRateLimits` (sixth bad try → 429)
- [ ] **Step 2: Run** `go test ./internal/web/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Hooks call the engine through a small interface (`hookEngine`) so the tests can fake it.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): auth and verified webhooks`.

### Task 13: JSON API for the UI

**Files:** Create `api/internal/web/api.go`, `api/internal/web/views.go`, `api/internal/web/api_test.go`. Also `web/src/api/types.ts` (hand-written mirror of the views below, kept in the same commit).

**Interfaces:**
- Produces (all JSON, auth required):
  - `GET /api/queue` → `{needs:[OrderView], processing:[OrderView], stats:{balance:int, autoToday:int, sentToday:int}}`
    - `needs` sorted by `deadlineAt` asc, nulls (direct) last
  - `GET /api/orders/{id}` → `OrderDetail` = `OrderView` + `attempts:[Attempt]` + `options:[SkuOption{sku, seller, price, customerNo|null, templateError|null, margin|null}]`
  - `POST /api/orders/{id}/buy` `{sku, customerNo, noCap}` → `BuyManual`
  - `POST /api/orders/{id}/reject` `{reason, force}`
  - `POST /api/orders/{id}/cancel`
  - `POST /api/orders/direct` `DirectReq` → `{id}`
  - `GET /api/history?source=itemku|direct&q=&before=<unix>` → 50 rows, `q` matches `order_number` or `customer_no`
  - `GET /api/products` → `[Group{game, seller, template, items:[ProductView{id,name,mode,offReason,deadlineClass,stockLocked,cost,price,net,margin,skus:[string]}]}]`
    - an unlinked product is in group `seller:""`
  - `PATCH /api/products/{id}` `{mode?, deadlineClass?, skus?}`
  - `POST /api/groups/mode` `{game, seller, mode}` (bulk `SetMode`; returns per-item errors)
  - `PUT /api/templates` `{seller, game, template}`: validates with `tmpl.Keys` non-empty
  - `GET /api/pricelist?q=` → 30 matches on brand/product_name/sku
  - `GET /api/settings`, `PUT /api/settings` (`min_margin`, `fee_pct`, `balance_alert`), `POST /api/settings/telegram-test`
  - `GET /api/status` → `{lastCallback, lastPoll, lastPriceSync, lastErrors:{itemku, digiflazz, telegram}}`
  - `OrderView`:
    - `{id, source, orderNumber, game, product, quantity, info:{[k]:string}, customerNo, sku, seller, cost, income, margin, state, reason, detail, deadlineAt, deadlineClass, createdAt, buyerWa}`
    - plus `whatsappUrl` when direct + delivered

- [ ] **Step 1: Failing tests** (an `httptest` server over a real engine with fakes):
  - `TestQueueSortedByDeadline`
  - `TestOrderOptionsRenderPerSellerTemplate`: two SKUs, seller A `{player_id}|{zone_id}`, seller B `{player_id}{zone_id}` → options `customerNo` `12345678|1234` and `123456781234`
  - `TestGroupModeBulk`
  - `TestTemplateRejectsNoPlaceholder`
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): UI JSON endpoints`.

### Task 14: Server main, embed and deploy files

**Files:** Create `api/cmd/server/main.go`, `api/internal/web/static.go` (`//go:embed all:dist`, with a `dist/.keep` placeholder), `deploy/Caddyfile`, `deploy/wongkito.service`, `deploy/README.md`

**Interfaces:**
- Consumes everything above.
- `main`:
  1. `config.Load`
  2. `store.Open`
  3. build clients; `Log` writes `api_log` with secrets redacted
  4. `engine.Run(ctx)`
  5. `http.Server{Addr: LISTEN_ADDR, ReadHeaderTimeout: 10s}`
  6. graceful shutdown on SIGTERM
  7. a daily goroutine deletes `api_log` rows older than 30 days

`deploy/README.md` covers:
- VPS with fixed IP; register the IP with itemku and whitelist it in Digiflazz
- DNS A record and the TXT record `v-api-itemku-{SHOPNAME}=1`
- set the itemku callback URL `https://<domain>/hooks/itemku`
- set the Digiflazz webhook URL `https://<domain>/hooks/digiflazz` with a secret
- generate the bcrypt hash: `htpasswd -bnBC 10 "" <pw> | tr -d ':\n'`
- nightly `sqlite3 $DB_PATH ".backup /var/backups/wongkito-$(date +%F).db"` via cron, plus an off-box copy
- `DRY_RUN=true` for the first days

- [ ] **Step 1: Write a smoke test** `api/cmd/server/main_test.go` `TestServerBoots`: start with a temp DB and fake env, `GET /` → 200 (serves `.keep`/index), `POST /hooks/digiflazz` without a signature → 401.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement** `main`, `static.go` and the deploy files. `Caddyfile`: `<domain> { reverse_proxy 127.0.0.1:8080 }`. The systemd unit uses `EnvironmentFile=/etc/wongkito.env` and `Restart=always`.
- [ ] **Step 4: Run** `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): server entrypoint and deploy`.

### Task 15: Web scaffold, tokens, API client, login and app shell

**Files:** Create `web/package.json`, `web/vite.config.ts`, `web/index.html`, `web/public/manifest.webmanifest` (+ `icon-192.png`, `icon-512.png`), `web/src/main.tsx`, `web/src/styles.css`, `web/src/api/client.ts`, `web/src/lib/format.ts`, `web/src/lib/format.test.ts`, `web/src/screens/Login.tsx`, `web/src/App.tsx`, `web/src/components/TabBar.tsx`

**Interfaces:**
- Consumes: `web/src/api/types.ts` (Task 13).
- Produces:
  - `api.get<T>(path)`, `api.send<T>(method, path, body?)`: on 401, switch to the Login screen. Errors throw `ApiError{status, message}`.
  - `formatRp(n: number): string` → `"Rp 1.284.500"`
  - `formatCountdown(secondsLeft: number): string` → `"1:42"`, `"9m"`, `"3h 12m"`, `"late"`
  - `urgency(deadlineAt, createdAt, now): 'calm'|'warn'|'late'`: `warn` when < 25% of the window is left
  - `TabBar`: Queue / Products / History / Settings, with the SVG icons from the mockup
  - Hash routes `#/queue|products|history|settings`. Add React Router only if this outgrows a 10-line switch.

`styles.css`:
- Tailwind v4 `@import "tailwindcss";` + `@theme` mapping the §9 tokens (`--color-desk` etc.)
- Geist and Geist Mono via Google Fonts
- `font-variant-numeric: tabular-nums` utility `.tn`
- `-webkit-font-smoothing: antialiased`
- `:active { transform: scale(.97) }` on buttons, 120 ms ease-out
- `prefers-reduced-motion` removes it

Vite dev proxy: `/api` and `/hooks` → `localhost:8080`. Manifest: `display: standalone`, `background_color` and `theme_color` `#F4F4F1`, plus iOS `apple-mobile-web-app-capable` meta tags.

- [ ] **Step 1: Failing Vitest tests** in `format.test.ts`:
  - `formatRp(1284500) === 'Rp 1.284.500'`
  - `formatCountdown(102) === '1:42'`
  - `urgency(now+50, now-550, now) === 'warn'` (10-min window, 50 s left)
  - `urgency(now-1, …) === 'late'`
- [ ] **Step 2: Run** `npm --prefix web test -- --run`. Expected: FAIL.
- [ ] **Step 3: Implement** the scaffold, format helpers, Login (password field + 48 px button), App shell with TabBar.
- [ ] **Step 4: Run** the tests, then `npm --prefix web run build`. Expected: PASS, and `web/dist/index.html` exists.
- [ ] **Step 5: Commit** `feat(web): scaffold, tokens, login, shell`.

### Task 16: Queue screen and focus card

**Files:** Create `web/src/screens/Queue.tsx`, `web/src/components/FocusCard.tsx`, `web/src/components/QueueRow.tsx`, `web/src/components/ConfirmSheet.tsx`, `web/src/components/FocusCard.test.tsx`

**Interfaces:**
- Consumes: `GET /api/queue`, `GET /api/orders/{id}`, `POST …/buy|reject|cancel`; the format helpers.
- Layout follows mockup B (right phone in `queue-direction-b.html`):
  - **Header:** "N to handle" (24 px / 600 / −0.03em), balance + "N auto today" right-aligned, and a "+ New order" button (opens Task 17's sheet).
  - **Focus card** = the first `needs` item, or the item the user tapped:
    - reason pill (`auto_failed` → "Auto failed", `--stop`; `manual_product` → "Manual", `--hold`; …)
    - product title (22 px / 600)
    - mono order number + age
    - countdown chip coloured by `urgency`
    - "Buyer gave" row: info key/values in mono
    - large editable mono `customerNo` (inset `--well`), with "Edit" in `--act`
    - seller / SKU / cost → margin row
    - plain-language reason line
    - 48 px primary **Buy & deliver**
    - "Other seller ▾": a list of `options`; picking one replaces `customerNo` with that option's rendered value; `templateError` options are disabled with the error shown
    - **Reject & refund**: `ConfirmSheet` with the reason list from Global Constraints. Default `WRONG_BUYER_INFORMATION` when the last rc is a wrong-number code, else `NO_STOCK`. Direct orders show **Cancel** instead.
    - **"Buy at a loss"** checkbox appears only when the selected option's margin < 0 (sends `noCap`)
  - **"Up next":** compact rows (14 px / 600 title, 12 px muted subline, right tag Manual/Pending/Failed).
  - **Empty state:** "Nothing needs you" + today's counts.
  - Refetch every 5 s (`refetchInterval: 5000`). Pause while the focus card's `customerNo` input has focus.

- [ ] **Step 1: Failing tests** (Vitest + Testing Library):
  - `pickingOtherSellerRewritesNumber`: options A `12345678|1234`, B `123456781234`; pick B → input value `123456781234`
  - `lossCheckboxOnlyWhenNegativeMargin`
  - `directOrderShowsCancelNotRefund`
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Use `vaul` `Drawer` for the sheets.
- [ ] **Step 4: Run** the tests. Then check the screen in a browser at 390×844 and at 1280 wide against the mockup. Expected: no overflow; a 44 px minimum on every tap target.
- [ ] **Step 5: Commit** `feat(web): queue and focus card`.

### Task 17: New direct order sheet

**Files:** Create `web/src/components/NewOrderSheet.tsx`, `web/src/components/NewOrderSheet.test.tsx`; modify `web/src/screens/Queue.tsx` (wire the button)

**Interfaces:**
- Consumes: `GET /api/products`, `GET /api/pricelist?q=`, `POST /api/orders/direct`, `GET /api/orders/{id}`.
- Behaviour:
  1. Search input across mapped products. A tab "Any Digiflazz SKU" switches to price-list search.
  2. For a mapped product: one input per template key (from the group's template), with the live rendered number underneath. Render client-side with a TS port of `Render` in `web/src/lib/tmpl.ts`, using the same filters `digits`/`msisdn`, and the Task 2 table as its test.
  3. For a raw SKU: one "Customer number" input.
  4. Optional fields: quantity (default 1), buyer name, buyer WhatsApp, price paid.
  5. Submit → poll the order until it is final → on `delivered`, show the SN(s) and a **Send to WhatsApp** button (`whatsappUrl`) or **Copy message** when no number was given.

- [ ] **Step 1: Failing tests:**
  - `web/src/lib/tmpl.test.ts` mirrors the Task 2 table
  - `rendersLiveCustomerNo`
  - `submitSendsDirectReq` (asserts the body shape matches `DirectReq`)
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): direct order sheet with whatsapp hand-off`.

### Task 18: Products screen

**Files:** Create `web/src/screens/Products.tsx`, `web/src/components/ModeControl.tsx`, `web/src/components/GroupSheet.tsx`, `web/src/components/ProductSheet.tsx`, `web/src/components/ModeControl.test.tsx`

**Interfaces:**
- Consumes: `GET /api/products`, `PATCH /api/products/{id}`, `POST /api/groups/mode`, `PUT /api/templates`, `GET /api/pricelist`.
- Layout follows `mockups/products.html`:
  - Filter chips All / Auto / Off / Unlinked with counts.
  - Group cards (radius 18): header shows game, seller, and the template in mono, plus a 36 px `•••` button.
  - Rows: name, `cost → price` with margin (`--sent`, or `--stop` if < 0), and the auto-off reason in `--hold` if `offReason` starts with `auto:`.
- `ModeControl`:
  - segmented `Off · Man · Auto`, 32 px visual inside a 44 px hit area
  - Auto selected = solid `--hi`; Off selected = `--stop` text
  - Auto disabled when there are no SKUs; Off disabled with the tooltip text "itemku won't let this item's stock change — turn it off in the itemku dashboard" when `stockLocked`
  - optimistic update with rollback on error
- `GroupSheet`: template input with a live example (from the first queue/history order of that game if any, else placeholders echo their names), "Set all to Auto", "Set all to Manual", "Turn all off on itemku" (with confirmation).
- `ProductSheet` (item tap):
  - deadline class radio Regular 24 h / 10 min / 2 min
  - SKU linking: price-list search, add, reorder (up/down buttons, no drag library), remove; the first SKU is labelled "Primary"

- [ ] **Step 1: Failing tests:**
  - `autoDisabledWhenUnlinked`
  - `offDisabledWhenStockLocked`
  - `rollsBackOnError` (`PATCH` returns 500 → previous mode re-selected)
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the tests and check visually at 390 px against the mockup. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): products, modes, templates, sku linking`.

### Task 19: History and Settings screens

**Files:** Create `web/src/screens/History.tsx`, `web/src/screens/Settings.tsx`, `web/src/screens/History.test.tsx`

**Interfaces:**
- Consumes: `GET /api/history`, `GET /api/orders/{id}`, `GET/PUT /api/settings`, `POST /api/settings/telegram-test`, `GET /api/status`, `POST /api/logout`.
- **History:**
  - source chips All / itemku / Direct and a search box (order number or customer number)
  - rows show product, mono customer number, state, time
  - tap opens a sheet with the attempts table (`ref_id`, SKU, status, rc, sn, price) and the WhatsApp button for delivered direct orders
  - "Load more" uses `before`
- **Settings:**
  - number inputs `min_margin`, `fee_pct`, `balance_alert`
  - Telegram test button
  - connection status list with relative times; any `lastErrors` shown in `--stop`
  - a static callout: "SKUs used in Auto must have Digiflazz backup sellers turned off"
  - Logout

- [ ] **Step 1: Failing test** `searchQueriesHistory` (typing `OD000045` calls `/api/history?q=OD000045` after a 300 ms debounce).
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `make test && make build`. Expected: all PASS and `bin/wongkito` is built.
- [ ] **Step 5: Commit** `feat(web): history and settings`.

### Task 20: Sandbox and dry-run verification

**Files:** Create `docs/superpowers/plans/2026-10-23-verification-log.md`, where the results are recorded.

This task is manual, against real services. Each step records the outcome in the log.

- [ ] **Step 1: itemku sandbox.** Keys come from itemku Discord; the IP must be registered. Run the binary with `ITEMKU_BASE_URL=https://sandbox-api-tokoku.itemku.com` and `DRY_RUN=true`. `POST /dev/api/product/generate`, then `/dev/api/order/generate` (`game_id 51`, `item_type_id 95`). Expected: the poll picks it up within 30 s, and it appears in Queue as `no_mapping`.
- [ ] **Step 2: Link the sandbox product** to a Digiflazz SKU and set the template. Retry in the UI → the Digiflazz call is sent with `testing:true` → `Sukses` → the DELIVER call is logged and not sent (dry run). Then repeat with `DRY_RUN=false` against the sandbox → itemku order status `DELIVERED`.
- [ ] **Step 3: Digiflazz webhook.** Trigger a `testing:true` transaction that returns `Pending` (use the Digiflazz test-case customer numbers) and confirm the webhook moves it to delivered. Also confirm a tampered signature returns 401 in the `api_log`.
- [ ] **Step 4: Production, `DRY_RUN=true`.**
  - DNS TXT, callback URL, Digiflazz IP whitelist and webhook set
  - one real cheap order arrives via the callback (`last_callback` updates)
  - the converted number is correct
  - no money moves
- [ ] **Step 5: Production live.** Set `DRY_RUN=false`, put one product in Auto, and place one real order. Check: Digiflazz `Sukses`, itemku `DELIVERED`, no Telegram alert, margin correct in History. Then enable the rest gradually.
- [ ] **Step 6: Commit** the verification log.
