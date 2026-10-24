-- itemku charges the Iklan Tokoku ad fee from a separate ads balance, so it is
-- not part of order_income and must be subtracted from it for cost decisions.
ALTER TABLE orders ADD COLUMN ads_fee INTEGER NOT NULL DEFAULT 0;
