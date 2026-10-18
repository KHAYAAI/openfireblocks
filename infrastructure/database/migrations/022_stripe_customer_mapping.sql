-- Where a customer's payment identity lives.
--
-- The billing sweep has been raising invoices on a schedule and stopping
-- there. ChargeInvoice existed and worked, but it takes the Stripe
-- customer id as an argument and nothing stored one -- so collecting
-- payment was only ever possible through an HTTP call where a human
-- supplied the id by hand. A billing system that raises invoices and
-- cannot collect them is an accounts-receivable spreadsheet.
--
-- Nullable, and it has to stay nullable. Not every customer pays through
-- Stripe: an enterprise on an annual licence is invoiced and pays by
-- transfer, which is most of the revenue this platform is priced for
-- (see docs/COMMERCIAL-MODEL.md). A NOT NULL here would either block
-- those customers from existing or force a fake id, and a fake id in a
-- payments column eventually gets charged.
--
-- The sweep treats NULL as "invoice raised, collection is manual" rather
-- than as an error, and reports it, so the finance team can see which
-- invoices they need to chase.
ALTER TABLE customers
  ADD COLUMN IF NOT EXISTS stripe_customer_id VARCHAR(255);

-- Unique where present.
--
-- Two of our customers mapped to one Stripe customer means one of them
-- gets charged for the other's usage, and it is the kind of mistake that
-- is only discovered by the party who was overcharged. A partial index
-- rather than a plain UNIQUE constraint, because NULL is the normal state
-- for the invoice-and-transfer customers above and several of them must
-- coexist.
CREATE UNIQUE INDEX IF NOT EXISTS customers_stripe_customer_id_key
  ON customers (stripe_customer_id)
  WHERE stripe_customer_id IS NOT NULL;

-- What a collection attempt did.
--
-- Separate from invoices rather than a status column on them, because an
-- invoice has one state and collection has many attempts. A card declines,
-- the customer updates it, the next sweep retries: that is three rows of
-- history and one invoice, and squashing it into the invoice loses exactly
-- the trail a chargeback dispute needs.
CREATE TABLE IF NOT EXISTS invoice_charges (
  charge_id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  invoice_id        UUID NOT NULL REFERENCES invoices(invoice_id),
  customer_id       UUID NOT NULL REFERENCES customers(customer_id),
  -- Stripe's PaymentIntent id. Null when the attempt failed before Stripe
  -- was reached at all, which is a different failure from a decline and
  -- wants to be distinguishable at 3am.
  payment_intent_id VARCHAR(255),
  amount_cents      INT NOT NULL,
  currency          VARCHAR(10) NOT NULL,
  status            VARCHAR(50) NOT NULL
                      CHECK (status IN ('succeeded', 'requires_action', 'failed', 'skipped')),
  -- Why, in the words the processor used. Kept verbatim: a paraphrased
  -- decline reason is useless when the customer's bank asks which code
  -- they sent.
  detail            TEXT,
  attempted_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS invoice_charges_invoice_id_idx
  ON invoice_charges (invoice_id, attempted_at DESC);

CREATE INDEX IF NOT EXISTS invoice_charges_customer_id_idx
  ON invoice_charges (customer_id, attempted_at DESC);

-- Row-level security, matching migration 011's treatment of every other
-- tenant-scoped table.
--
-- Charges name a customer and an amount. Without this a tenant querying
-- through the app role could read another tenant's payment history, which
-- is both a leak and a competitive one -- invoice amounts are revenue
-- figures.
ALTER TABLE invoice_charges ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_charges FORCE ROW LEVEL SECURITY;

-- Named and shaped exactly like migration 011's policies, using the same
-- current_customer_id() helper. A second spelling of the same rule is a
-- second thing to keep correct, and the one that drifts is the one nobody
-- is looking at.
DROP POLICY IF EXISTS tenant_isolation ON invoice_charges;
CREATE POLICY tenant_isolation ON invoice_charges
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

-- Same grants migration 011 gives every other tenant-scoped table, so the
-- app role can actually use this one.
GRANT SELECT, INSERT, UPDATE, DELETE ON invoice_charges TO app;
