package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A database created before migrations existed (schema 001 applied directly, with data)
// upgrades in place: data survives and old usage is credited to its owner.
func TestUpgradeFromPreMigrationDatabase(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "compute-box.db")))
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := migrations.ReadFile("migrations/001_initial.sql")
	for _, stmt := range []string{
		string(initial),
		`INSERT INTO users (name, created_at) VALUES ('default', '2026-11-13T00:00:00Z')`,
		`INSERT INTO machines (user_id, name, last_seen) VALUES (1, 'dentist-front-desk', '2026-11-13T00:00:00Z')`,
		`INSERT INTO jobs (id, user_id, project, operation, machine, input, definition, status, created_at)
		 VALUES ('job_1', 1, 'clinic', 'lookup', 'dentist-front-desk', '{}', '{}', 'succeeded', '2026-11-13T00:00:00Z')`,
		`INSERT INTO model_usage (job_id, model, input_tokens, cached_tokens, cache_write_tokens, output_tokens,
		 reasoning_tokens, cost_usd, cost_conservative_usd, created_at)
		 VALUES ('job_1', 'gpt-6-luna', 10, 0, 0, 5, 0, 0.25, 0.30, '2026-11-13T00:00:00Z')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	for i := 0; i < 2; i++ { // the second open must be a no-op
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		ctx := context.Background()
		if s.DefaultUserID() != 1 {
			t.Fatalf("owner id changed: %d", s.DefaultUserID())
		}
		spend, err := s.UserSpend(ctx, 1)
		if err != nil || spend.Calls != 1 || spend.CostConservativeUSD != 0.30 {
			t.Fatalf("old usage not credited to the owner: %+v %v", spend, err)
		}
		machines, _ := s.ListMachines(ctx, 1)
		if len(machines) != 1 || (i == 0) != (machines[0].TokenPrefix == "") {
			t.Fatalf("machines after upgrade: %+v", machines)
		}
		if j, _ := s.GetJob(ctx, 1, "job_1"); j == nil {
			t.Fatal("job lost in upgrade")
		}
		// An existing machine gets a token through the dashboard without losing anything.
		if _, err := s.IssueMachineToken(ctx, 1, "dentist-front-desk"); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}
