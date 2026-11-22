package routes_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"sonic-screwdriver/compute-box/src/routes"
	"sonic-screwdriver/compute-box/src/services/budget"
	"sonic-screwdriver/compute-box/src/services/hub"
	"sonic-screwdriver/compute-box/src/store"
)

// The dashboard shows what the SDK synced on one page, escapes it, and can't change it.
func TestDashboardShowsSyncedConfigurationReadOnly(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, _ := budget.New(context.Background(), db, 3, nil)
	srv := httptest.NewServer(routes.New(routes.Deps{Store: db, Hub: hub.New(db, "secret"), Ledger: ledger}))
	defer srv.Close()

	expect(t, do(t, "GET", srv.URL+"/", "", nil), 200, "No projects yet")

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
		`<span class="value">0 / 1</span>`, // machines online
		"No jobs yet.",
	} {
		expect(t, do(t, "GET", srv.URL+"/", "", nil), 200, want)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if res := do(t, method, srv.URL+"/", "", nil); res.StatusCode != 405 {
			t.Fatalf("%s /: want 405, got %d", method, res.StatusCode)
		}
	}
}
