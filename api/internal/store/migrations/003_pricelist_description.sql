-- Digiflazz's per-SKU seller description ("desc"), which says the customer
-- number format the seller expects (e.g. "Tujuan = ID + ID Server").
ALTER TABLE pricelist ADD COLUMN description TEXT NOT NULL DEFAULT '';
