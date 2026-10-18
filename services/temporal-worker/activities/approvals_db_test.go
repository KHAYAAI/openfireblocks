package activities

// The approval rules run against a real Postgres with migration 023
// applied -- the triggers are the rules, so a mock would test nothing.
// Skips when no database is reachable, like key_rotation_db_test.go.
//
//	eval "$(../../infrastructure/local/postgres-local.sh start)"
//	DATABASE_URL=$DATABASE_ADMIN_URL TENANT_DATABASE_URL=<app dsn> go test -run Approval ./activities/

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"

	"forge-crypto/temporal-worker/workflows"
)

type approvalFixture struct {
	admin, tenant *sql.DB
	customer      string
	initiator     string
	approvers     []string // role approver
	operator      string   // role operator: may not approve
	outsider      string   // no role in this customer
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	admin := requireLiveDB(t)
	tenantDSN := os.Getenv("TENANT_DATABASE_URL")
	if tenantDSN == "" {
		tenantDSN = strings.Replace(os.Getenv("DATABASE_URL"), "app_admin:", "app:", 1)
	}
	tenant, err := sql.Open("postgres", tenantDSN)
	if err != nil || tenant.Ping() != nil {
		t.Skip("tenant (app) role not reachable")
	}
	var exists bool
	if err := admin.QueryRow(`SELECT to_regclass('approval_requests') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Skip("migration 023 not applied")
	}

	f := &approvalFixture{admin: admin, tenant: tenant}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(admin.QueryRow(`INSERT INTO customers (name, api_key_hash)
		VALUES ('approvals-test', decode(md5(clock_timestamp()::text || random()::text), 'hex'))
		RETURNING customer_id`).Scan(&f.customer))

	user := func(tag, role string) string {
		var id string
		must(admin.QueryRow(`INSERT INTO users (email, password_hash, full_name)
			VALUES ($1, 'x', $2) RETURNING id`,
			fmt.Sprintf("%s-%s@example.test", tag, f.customer[:8]), tag).Scan(&id))
		if role != "" {
			_, err := admin.Exec(`INSERT INTO user_customer_roles (user_id, customer_id, role) VALUES ($1, $2, $3)`, id, f.customer, role)
			must(err)
		}
		return id
	}
	// The initiator is an admin: the strongest case, since admins may
	// otherwise approve.
	f.initiator = user("initiator", "admin")
	f.approvers = []string{user("alice", "approver"), user("bob", "approver"), user("carol", "admin")}
	f.operator = user("oscar", "operator")
	f.outsider = user("mallory", "")
	t.Cleanup(func() { admin.Close(); tenant.Close() })
	return f
}

func (f *approvalFixture) acts() *Activities { return &Activities{db: f.admin} }

func (f *approvalFixture) open(t *testing.T, wf string, initiator string) workflows.OpenApprovalRequestResult {
	t.Helper()
	res, err := f.acts().OpenApprovalRequest(context.Background(), workflows.OpenApprovalRequestInput{
		WorkflowID: wf,
		Request: workflows.TransactionRequest{CustomerID: f.customer, ChainID: 1, To: "0xabc", Value: "20000000000000000000",
			InitiatedByUserID: initiator, InitiatedByLabel: "initiator@example.test"},
		Reasons: []string{"high-value transaction (> 10 ETH) requires approval"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return *res
}

// decide writes a decision the way the gateway does: as the tenant role,
// with the tenant's id set for row-level security.
func (f *approvalFixture) decide(approvalID, user, decision string) error {
	tx, err := f.tenant.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT set_config('app.current_customer_id', $1, true)`, f.customer); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO approval_decisions (approval_id, customer_id, user_id, decision, step_up)
		VALUES ($1, $2, $3, $4, 'totp')`, approvalID, f.customer, user, decision); err != nil {
		return err
	}
	return tx.Commit()
}

func sqlState(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

func (f *approvalFixture) status(t *testing.T, wf string) workflows.RecordedApprovalStatus {
	t.Helper()
	s, err := f.acts().GetRecordedApprovalStatus(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	return *s
}

func TestApprovalRequestOpensWithDualControlByDefaultAndIsIdempotent(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-default"
	first := f.open(t, wf, f.initiator)
	if first.RequiredApprovals != 2 {
		t.Fatalf("default required approvals = %d, want 2", first.RequiredApprovals)
	}
	if first.ExpiresInSeconds < 3500 || first.ExpiresInSeconds > 3600 {
		t.Fatalf("default window = %ds, want about an hour", first.ExpiresInSeconds)
	}
	again := f.open(t, wf, f.initiator)
	if again.ApprovalID != first.ApprovalID {
		t.Fatal("a retried open created a second approval request")
	}
}

// The policy's terms are fixed when a request opens. Lowering the bar
// afterwards does not lower it for a transfer already waiting.
func TestLoweringThePolicyDoesNotLowerTheBarForAWaitingTransfer(t *testing.T) {
	f := newApprovalFixture(t)
	if _, err := f.admin.Exec(`INSERT INTO approval_policies (customer_id, required_approvals, window_minutes) VALUES ($1, 3, 30)`, f.customer); err != nil {
		t.Fatal(err)
	}
	wf := "settlement-" + f.customer + "-policy"
	opened := f.open(t, wf, f.initiator)
	if opened.RequiredApprovals != 3 || opened.ExpiresInSeconds > 1800 {
		t.Fatalf("opened with %d approvals / %ds, want 3 / 30min", opened.RequiredApprovals, opened.ExpiresInSeconds)
	}
	if _, err := f.admin.Exec(`UPDATE approval_policies SET required_approvals = 1 WHERE customer_id = $1`, f.customer); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(opened.ApprovalID, f.approvers[0], "approve"); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t, wf); st.Status != "pending" {
		t.Fatalf("one approval approved a transfer opened needing three (status %s)", st.Status)
	}
	// And the terms themselves cannot be edited.
	_, err := f.admin.Exec(`UPDATE approval_requests SET required_approvals = 1 WHERE workflow_id = $1`, wf)
	if sqlState(err) != "OFB04" {
		t.Fatalf("editing a request's terms: got %v", err)
	}
}

func TestTwoDistinctApproversApproveInTheDatabase(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-two"
	opened := f.open(t, wf, f.initiator)
	if err := f.decide(opened.ApprovalID, f.approvers[0], "approve"); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t, wf); st.Status != "pending" || st.DistinctApprovals != 1 {
		t.Fatalf("after one approval: %+v", st)
	}
	if err := f.decide(opened.ApprovalID, f.approvers[2], "approve"); err != nil { // an admin who is not the initiator
		t.Fatal(err)
	}
	if st := f.status(t, wf); st.Status != "approved" || st.DistinctApprovals != 2 {
		t.Fatalf("after two approvals: %+v", st)
	}
	// Closed: nobody decides after that.
	if err := f.decide(opened.ApprovalID, f.approvers[1], "reject"); sqlState(err) != "OFB03" {
		t.Fatalf("deciding on a closed request: %v", err)
	}
}

func TestTheInitiatorCannotDecideEvenAsAnAdmin(t *testing.T) {
	f := newApprovalFixture(t)
	opened := f.open(t, "settlement-"+f.customer+"-self", f.initiator)
	for _, d := range []string{"approve", "reject"} {
		if err := f.decide(opened.ApprovalID, f.initiator, d); sqlState(err) != "OFB01" {
			t.Fatalf("initiator %s: got %v, want segregation-of-duties error", d, err)
		}
	}
}

func TestOnlyApproversAndAdminsOfThisOrganisationCanDecide(t *testing.T) {
	f := newApprovalFixture(t)
	opened := f.open(t, "settlement-"+f.customer+"-roles", f.initiator)
	for name, u := range map[string]string{"operator": f.operator, "no role": f.outsider} {
		if err := f.decide(opened.ApprovalID, u, "approve"); sqlState(err) != "OFB02" {
			t.Fatalf("%s approving: got %v", name, err)
		}
	}
}

func TestEachPersonDecidesOnceAndDecisionsCannotBeChanged(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-once"
	opened := f.open(t, wf, f.initiator)
	if err := f.decide(opened.ApprovalID, f.approvers[0], "approve"); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(opened.ApprovalID, f.approvers[0], "approve"); sqlState(err) != "23505" {
		t.Fatalf("second decision by the same person: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE approval_decisions SET decision = 'reject' WHERE approval_id = $1`,
		`DELETE FROM approval_decisions WHERE approval_id = $1`,
		`DELETE FROM approval_requests WHERE approval_id = $1`,
	} {
		if _, err := f.admin.Exec(stmt, opened.ApprovalID); sqlState(err) != "OFB04" {
			t.Fatalf("%s: got %v, want refusal even for app_admin", stmt, err)
		}
	}
}

func TestOneRejectionRejectsInTheDatabase(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-reject"
	opened := f.open(t, wf, f.initiator)
	_ = f.decide(opened.ApprovalID, f.approvers[0], "approve")
	if err := f.decide(opened.ApprovalID, f.approvers[1], "reject"); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t, wf); st.Status != "rejected" {
		t.Fatalf("status %s after a rejection", st.Status)
	}
}

// The bypass the guard exists for: marking a request approved directly,
// without the decisions. Refused even for the BYPASSRLS admin role.
func TestARequestCannotBeMarkedApprovedWithoutTheApprovals(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-bypass"
	f.open(t, wf, f.initiator)
	_, err := f.admin.Exec(`UPDATE approval_requests SET status = 'approved' WHERE workflow_id = $1`, wf)
	if sqlState(err) != "OFB01" {
		t.Fatalf("direct approval: got %v", err)
	}
}

func TestAnExpiredRequestCannotBeDecided(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-expired"
	opened := f.open(t, wf, f.initiator)
	if err := f.acts().CloseApprovalRequest(context.Background(), workflows.CloseApprovalRequestInput{WorkflowID: wf, Status: "expired"}); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t, wf); st.Status != "expired" {
		t.Fatalf("status %s", st.Status)
	}
	if err := f.decide(opened.ApprovalID, f.approvers[0], "approve"); sqlState(err) != "OFB03" {
		t.Fatalf("deciding after expiry: %v", err)
	}
}

// Another tenant's session sees none of it.
func TestAnotherTenantCannotSeeOrDecide(t *testing.T) {
	f := newApprovalFixture(t)
	opened := f.open(t, "settlement-"+f.customer+"-rls", f.initiator)
	other := newApprovalFixture(t)

	tx, _ := f.tenant.Begin()
	defer tx.Rollback()
	_, _ = tx.Exec(`SELECT set_config('app.current_customer_id', $1, true)`, other.customer)
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM approval_requests WHERE approval_id = $1`, opened.ApprovalID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("another tenant can see this tenant's approval request")
	}
	_, err := tx.Exec(`INSERT INTO approval_decisions (approval_id, customer_id, user_id, decision, step_up)
		VALUES ($1, $2, $3, 'approve', 'totp')`, opened.ApprovalID, f.customer, f.approvers[0])
	if err == nil {
		t.Fatal("another tenant's session wrote a decision")
	}
}

// An API-key-initiated transfer has no person to exclude; approvers count.
func TestAMachineInitiatedTransferStillNeedsTheQuorum(t *testing.T) {
	f := newApprovalFixture(t)
	wf := "settlement-" + f.customer + "-machine"
	opened := f.open(t, wf, "")
	_ = f.decide(opened.ApprovalID, f.approvers[0], "approve")
	if st := f.status(t, wf); st.Status != "pending" {
		t.Fatalf("one approval approved a machine-initiated transfer: %s", st.Status)
	}
	_ = f.decide(opened.ApprovalID, f.initiator, "approve") // an admin, and not the initiator here
	if st := f.status(t, wf); st.Status != "approved" {
		t.Fatalf("status %s", st.Status)
	}
}
