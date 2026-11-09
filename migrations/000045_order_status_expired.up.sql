SET LOCAL lock_timeout = '5s';
-- Terminal order state set only by the system (checkout-expiry worker or a
-- provider-reported expiry) when an order was never paid. Plain ALTER TYPE
-- (not a DO block) so sqlc generates dbgen.OrderStatusEXPIRED.
ALTER TYPE order_status ADD VALUE IF NOT EXISTS 'EXPIRED';
