export type OrderSource = 'itemku' | 'direct';
export type OrderState =
  | 'new'
  | 'buying'
  | 'processing'
  | 'delivering'
  | 'delivered'
  | 'needs_you'
  | 'rejected'
  | 'cancelled';
export type ProductMode = 'off' | 'manual' | 'auto';
export type DeadlineClass = 'regular' | 'ten_min' | 'instant';

export interface OrderView {
  id: string;
  source: OrderSource;
  orderNumber: string;
  game: string;
  product: string;
  quantity: number;
  info: Record<string, string>;
  customerNo: string;
  sku: string;
  seller: string;
  cost: number;
  income: number; // after itemku's fee and the Iklan Tokoku ad fee
  adsFee: number;
  margin: number;
  state: OrderState;
  reason: string;
  detail: string;
  deadlineAt: number | null;
  deadlineClass: DeadlineClass;
  createdAt: number;
  buyerWa: string;
  whatsappUrl?: string;
}

export interface Attempt {
  refId: string;
  unit: number;
  attemptNo: number;
  sku: string;
  customerNo: string;
  status: 'unknown' | 'Pending' | 'Sukses' | 'Gagal';
  rc: string;
  message: string;
  sn: string;
  price: number;
  createdAt: number;
}

export interface SkuOption {
  sku: string;
  seller: string;
  price: number;
  customerNo: string | null;
  templateError: string | null;
  margin: number | null;
}

export interface OrderDetail extends OrderView {
  attempts: Attempt[];
  options: SkuOption[];
}

export interface QueueStats {
  balance: number | null; // null until the balance loop has fetched it
  autoToday: number;
  sentToday: number;
}

export interface QueueResponse {
  needs: OrderView[];
  processing: OrderView[];
  stats: QueueStats;
}

export interface ProductView {
  id: number;
  name: string;
  mode: ProductMode;
  offReason: string;
  deadlineClass: DeadlineClass;
  stockLocked: boolean;
  cost: number;
  price: number;
  stock: number; // itemku stock from the last catalog sync
  net: number;
  margin: number;
  skus: string[];
}

export interface ProductGroup {
  game: string;
  seller: string;
  template: string;
  items: ProductView[];
}

export interface PricelistItem {
  sku: string;
  productName: string;
  brand: string;
  category: string;
  type: string;
  seller: string;
  price: number;
  stock: number;
  unlimitedStock: boolean;
  multi: boolean;
  buyerProductStatus: boolean;
  sellerProductStatus: boolean;
}

export interface Settings {
  min_margin: number;
  fee_pct: number;
  balance_alert: number;
}

export interface StatusErrors {
  itemku: string;
  digiflazz: string;
  telegram: string;
}

export interface Status {
  lastCallback: number;
  lastPoll: number;
  lastPriceSync: number;
  lastErrors: StatusErrors;
}

export interface DirectReq {
  productId?: number;
  sku?: string;
  fields?: Record<string, string>;
  customerNo?: string;
  quantity?: number;
  buyerName?: string;
  buyerWa?: string;
  paidPrice?: number;
  clientId?: string; // idempotency key: a retried submit never buys twice
}

// error is set when the order was created but the buy step failed.
export interface DirectResult {
  id: string;
  error?: string;
}

export interface BuyReq {
  sku?: string;
  customerNo?: string;
  noCap?: boolean;
}

export interface RejectReq {
  reason: string;
  force?: boolean;
}

export interface CancelReq {
  force?: boolean;
}

export interface ProductPatch {
  mode?: ProductMode;
  deadlineClass?: DeadlineClass;
  skus?: string[];
}

export interface GroupModeReq {
  game: string;
  seller: string;
  mode: ProductMode;
}

export interface GroupModeResult {
  id: number;
  error?: string;
}

export interface GroupModeResponse {
  results: GroupModeResult[];
}

export interface TemplateReq {
  seller: string;
  game: string;
  template: string;
}
