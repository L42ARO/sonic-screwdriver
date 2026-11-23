package routes_test

import (
	"net/http/httptest"
	"testing"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/budget"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
)

// The dashboard shows what the SDK synced on one page, escapes it, and can't change it.
func TestDashboardShowsSyncedConfigurationReadOnly(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger := budget.New(db, budget.NewCapLimiter(db, 3, nil))
	srv := httptest.NewServer(routes.New(routes.Deps{Store: db, Hub: hub.New(db), Ledger: ledger}))
	defer srv.Close()
	owner := newOwner(t, db)
	page := map[string]string{"Cookie": owner.cookie}

	expect(t, do(t, "GET", srv.URL+"/", "", page), 200, "No projects yet")

	expect(t, do(t, "PUT", srv.URL+"/v1/machines/front-desk/programs",
		`{"dentasoft":{"executable":"C:\\Apps\\DentaSoft.exe","args":["--clinic","main"],"window_title_contains":"DentaSoft"}}`, nil), 200, "dentasoft")
	expect(t, do(t, "PUT", srv.URL+"/v1/projects/clinic/programs",
		`{"dentasoft":{"description":"Patient records","instructions":"Log in as <drsmith>"}}`, nil), 200, "dentasoft")
	expect(t, do(t, "PUT", srv.URL+"/v1/projects/clinic/operations",
		`[{"name":"lookup_patient","description":"Look up a patient.","instructions":"Use DentaSoft.\n\nFind them.",
		  "input":{"type":"object","properties":{"patient_id":{"type":"string"}}},"output":{"type":"object"}}]`, nil), 200, "lookup_patient")

	for _, want := range []string{
		`1 operations · 1 programs · 0 jobs`, // project summary
		"Look up a patient.",                 // operation
		"Log in as &lt;drsmith&gt;",          // program instructions, escaped
		`&#34;patient_id&#34;: {`,            // indented schema
		`C:\Apps\DentaSoft.exe`,              // machine installation
		"--clinic main",
		"never connected",
		`<span class="value">0 / 2</span>`, // machines online: front-desk (synced) and dentist-front-desk (token)
		"No jobs yet.",
	} {
		expect(t, do(t, "GET", srv.URL+"/", "", page), 200, want)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if res := do(t, method, srv.URL+"/", "", nil); res.StatusCode != 405 {
			t.Fatalf("%s /: want 405, got %d", method, res.StatusCode)
		}
	}
}
