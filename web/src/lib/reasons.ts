import type { OrderView } from '../api/types';

// Order.reason values from api/internal/relay/engine.go.
const LABEL: Record<string, string> = {
  auto_failed: 'Auto failed',
  manual_product: 'Manual',
  partial: 'Partial',
  multi_blocked: 'Multi-unit',
  template_error: 'Template error',
  unsupported_type: 'Unsupported',
  deliver_failed: 'Deliver failed',
  no_mapping: 'Not linked',
};

export const reasonLabel = (reason: string) => LABEL[reason] ?? reason.replace(/_/g, ' ');

// Manual is expected work (hold); everything else needs attention (stop).
export const reasonTone = (o: OrderView) => (o.reason === 'manual_product' ? 'hold' : 'stop');
