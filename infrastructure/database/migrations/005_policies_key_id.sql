-- services/policy binds every policy to one key (CreatePolicyRequest.KeyID,
-- ListPoliciesByKey), but the policies table had no key_id column at all.
ALTER TABLE policies
  ADD COLUMN key_id UUID REFERENCES key_pairs(key_id);

CREATE INDEX idx_policies_key ON policies(key_id);
