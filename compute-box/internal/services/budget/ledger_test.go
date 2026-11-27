package budget

import (
	"context"
	"testing"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// Each user is held to their own cap; one user's spend never blocks another.
func TestCapIsPerUser(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	alice, _ := db.EnsureUser(ctx, "alice")
	bob, _ := db.EnsureUser(ctx, "bob")
	ledger := New(db, NewCapLimiter(db, 0.01, nil))

	if err := ledger.Allow(ctx, alice); err != nil {
		t.Fatalf("fresh user refused: %v", err)
	}
	for ledger.Allow(ctx, alice) == nil {
		ledger.Record(ctx, alice, "", "gpt-6-luna", Tokens{Input: 200_000, Output: 20_000})
	}
	if err := ledger.Allow(ctx, bob); err != nil {
		t.Fatalf("alice's spend blocked bob: %v", err)
	}
	spent, capUSD, _ := ledger.Budget(ctx, alice)
	if spent < 0.01 || capUSD != 0.01 {
		t.Fatalf("alice budget = %v of %v", spent, capUSD)
	}
	if spent, _, _ := ledger.Budget(ctx, bob); spent != 0 {
		t.Fatalf("bob charged %v for alice's calls", spent)
	}
	if err := New(db, NewCapLimiter(db, 0, nil)).Allow(ctx, alice); err != nil {
		t.Fatalf("a cap of 0 should mean no limit: %v", err)
	}
}

// A user's own cap overrides the server default, takes effect at once, and can be removed.
func TestOwnCapOverridesDefault(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	carol, _ := db.EnsureUser(ctx, "carol")
	ledger := New(db, NewCapLimiter(db, 0.01, nil))
	for ledger.Allow(ctx, carol) == nil {
		ledger.Record(ctx, carol, "", "gpt-6-luna", Tokens{Input: 200_000, Output: 20_000})
	}
	raise := 100.0
	if err := db.SetUserBudget(ctx, "carol", &raise); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Allow(ctx, carol); err != nil {
		t.Fatalf("raised cap still refused: %v", err)
	}
	if _, capUSD, _ := ledger.Budget(ctx, carol); capUSD != 100 {
		t.Fatalf("cap = %v, want 100", capUSD)
	}
	none := 0.0
	db.SetUserBudget(ctx, "carol", &none)
	if err := ledger.Allow(ctx, carol); err != nil {
		t.Fatalf("own cap of 0 should mean no limit: %v", err)
	}
	db.SetUserBudget(ctx, "carol", nil)
	if err := ledger.Allow(ctx, carol); err == nil {
		t.Fatal("back on the default cap, carol should be refused again")
	}
	if err := db.SetUserBudget(ctx, "nobody", &raise); err == nil {
		t.Fatal("setting a budget for an unknown user should fail")
	}
	rows, err := db.UserBudgets(ctx)
	if err != nil || len(rows) < 2 {
		t.Fatalf("UserBudgets = %v, %v", rows, err)
	}
	for _, r := range rows {
		if r.Name == "carol" && (r.Custom || r.SpentUSD < 0.01) {
			t.Fatalf("carol row = %+v", r)
		}
	}
}
