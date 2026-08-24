package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// PostgresDB holds two connections at two different privilege levels,
// mirroring services/api-gateway/src/database/postgres.service.ts's split
// between its RLS-scoped pool and its admin pool:
//
//   - admin (app_admin, BYPASSRLS): used ONLY to resolve which customer a
//     key_id/policy_id belongs to -- an unavoidable privileged step, since
//     RLS can't be applied to a query before the tenant it should be
//     scoped to is known.
//   - tenant (app, RLS-enforced): used for every actual read/write of
//     policy data, wrapped in withTenant so migration 011's row-level
//     security policies actually apply.
//
// This replaces the previous app_admin-only connection (see git history)
// that this file's own comment used to describe as an interim measure
// "until it threads the customer_id it already receives per-request into
// the same set_config() pattern api-gateway uses" -- this is that follow-through.
type PostgresDB struct {
	admin  *sql.DB
	tenant *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	adminDSN := os.Getenv("DATABASE_URL")
	if adminDSN == "" {
		adminDSN = "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	tenantDSN := os.Getenv("DATABASE_TENANT_URL")
	if tenantDSN == "" {
		tenantDSN = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect admin pool: %w", err)
	}
	if err := admin.Ping(); err != nil {
		return nil, fmt.Errorf("admin pool ping failed: %w", err)
	}

	tenant, err := sql.Open("postgres", tenantDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect tenant pool: %w", err)
	}
	if err := tenant.Ping(); err != nil {
		return nil, fmt.Errorf("tenant pool ping failed: %w", err)
	}

	log.Printf("policy service connected to PostgreSQL (admin + RLS-scoped tenant pools)")
	return &PostgresDB{admin: admin, tenant: tenant}, nil
}

func (p *PostgresDB) Close() error {
	tenantErr := p.tenant.Close()
	adminErr := p.admin.Close()
	if tenantErr != nil {
		return tenantErr
	}
	return adminErr
}

// withTenant runs fn inside a transaction on the RLS-scoped tenant pool
// with app.current_customer_id set via set_config() for that
// transaction's duration -- SET LOCAL doesn't accept bind parameters, only
// set_config() does; the third argument (true) scopes it to the
// transaction, exactly like SET LOCAL would. Mirrors
// services/api-gateway/src/database/postgres.service.ts's withTenant().
func (p *PostgresDB) withTenant(ctx context.Context, customerID string, fn func(tx *sql.Tx) error) error {
	tx, err := p.tenant.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tenant transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_customer_id', $1, true)`, customerID); err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// resolveCustomerIDForKey looks up which customer owns keyID. Runs on the
// admin pool -- this is the one place per request that's genuinely
// unavoidable at app_admin privilege, since RLS can't scope a query to a
// tenant that isn't known yet. Everything downstream of this call uses
// withTenant instead.
func (p *PostgresDB) resolveCustomerIDForKey(ctx context.Context, keyID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM key_pairs WHERE key_id = $1::uuid`, keyID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("key %s not found", keyID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for key %s: %w", keyID, err)
	}
	return customerID, nil
}

// resolveCustomerIDForPolicy is resolveCustomerIDForKey's counterpart for
// operations addressed by policy_id instead of key_id.
func (p *PostgresDB) resolveCustomerIDForPolicy(ctx context.Context, policyID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM policies WHERE policy_id = $1::uuid`, policyID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("policy %s not found", policyID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for policy %s: %w", policyID, err)
	}
	return customerID, nil
}

// policyRules is what's actually stored in policies.rules (JSONB): the
// table has no dedicated columns for a policy's rule list, approval config,
// or rate limit, so all three are packed into that one column together.
type policyRules struct {
	Rules     []*PolicyRule    `json:"rules"`
	Approvals *ApprovalConfig  `json:"approvals,omitempty"`
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`
}

func (p *PostgresDB) CreatePolicy(ctx context.Context, policy *Policy) error {
	rules, err := json.Marshal(policyRules{Rules: policy.Rules, Approvals: policy.Approvals, RateLimit: policy.RateLimit})
	if err != nil {
		return fmt.Errorf("failed to marshal policy rules: %w", err)
	}

	customerID, err := p.resolveCustomerIDForKey(ctx, policy.KeyID)
	if err != nil {
		return err
	}

	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO policies (policy_id, customer_id, key_id, name, description, rules, status, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9)
		`, policy.PolicyID, customerID, policy.KeyID, policy.Name, policy.Description, rules, policy.Status, policy.CreatedAt, policy.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert policy: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	policy.CustomerID = customerID
	return nil
}

func (p *PostgresDB) GetPolicy(ctx context.Context, policyID string) (*Policy, error) {
	customerID, err := p.resolveCustomerIDForPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}

	var policy Policy
	var rulesRaw []byte
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT policy_id, key_id, name, COALESCE(description, ''), status, rules, created_at, updated_at
			FROM policies WHERE policy_id = $1::uuid
		`, policyID)
		return row.Scan(&policy.PolicyID, &policy.KeyID, &policy.Name, &policy.Description, &policy.Status, &rulesRaw, &policy.CreatedAt, &policy.UpdatedAt)
	})
	if err == sql.ErrNoRows {
		// Either the policy genuinely doesn't exist, or -- if
		// resolveCustomerIDForPolicy above somehow raced with a delete --
		// RLS is correctly hiding it from a tenant context it no longer
		// belongs to. Both cases are indistinguishable to the caller and
		// both should read as "not found," never as a different error
		// that might hint at cross-tenant existence.
		return nil, fmt.Errorf("policy %s not found", policyID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query policy: %w", err)
	}
	policy.CustomerID = customerID

	var pr policyRules
	if err := json.Unmarshal(rulesRaw, &pr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy rules: %w", err)
	}
	policy.Rules = pr.Rules
	policy.Approvals = pr.Approvals
	policy.RateLimit = pr.RateLimit
	return &policy, nil
}

func (p *PostgresDB) ListPoliciesByKey(ctx context.Context, keyID string) ([]*Policy, error) {
	customerID, err := p.resolveCustomerIDForKey(ctx, keyID)
	if err != nil {
		return nil, err
	}

	var policies []*Policy
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT policy_id, key_id, name, COALESCE(description, ''), status, rules, created_at, updated_at
			FROM policies WHERE key_id = $1::uuid
			ORDER BY created_at DESC
		`, keyID)
		if err != nil {
			return fmt.Errorf("failed to query policies: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var policy Policy
			var rulesRaw []byte
			if err := rows.Scan(&policy.PolicyID, &policy.KeyID, &policy.Name, &policy.Description, &policy.Status, &rulesRaw, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
				return fmt.Errorf("failed to scan policy row: %w", err)
			}
			var pr policyRules
			if err := json.Unmarshal(rulesRaw, &pr); err != nil {
				return fmt.Errorf("failed to unmarshal policy rules: %w", err)
			}
			policy.CustomerID = customerID
			policy.Rules = pr.Rules
			policy.Approvals = pr.Approvals
			policy.RateLimit = pr.RateLimit
			policies = append(policies, &policy)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return policies, nil
}

func (p *PostgresDB) UpdatePolicy(ctx context.Context, policy *Policy) error {
	rules, err := json.Marshal(policyRules{Rules: policy.Rules, Approvals: policy.Approvals, RateLimit: policy.RateLimit})
	if err != nil {
		return fmt.Errorf("failed to marshal policy rules: %w", err)
	}

	customerID, err := p.resolveCustomerIDForPolicy(ctx, policy.PolicyID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE policies SET name = $2, description = $3, rules = $4, status = $5, updated_at = $6
			WHERE policy_id = $1::uuid
		`, policy.PolicyID, policy.Name, policy.Description, rules, policy.Status, policy.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to update policy: %w", err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("policy %s not found", policy.PolicyID)
		}
		return nil
	})
}
