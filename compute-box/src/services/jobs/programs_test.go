package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"sonic-screwdriver/compute-box/src/store"
)

func TestListProgramsShowsOnlyProjectGuidanceAndInstallationAvailability(t *testing.T) {
	e := &env{
		programs: map[string]store.Program{
			"records": {Description: "Patient records", Instructions: "Use for patient lookups"},
			"viewer":  {Description: "Referral viewer"},
		},
		installed: map[string]store.InstalledProgram{
			"records": {Executable: `C:\Private\records.exe`},
			"billing": {Executable: `C:\Private\billing.exe`},
		},
	}
	res, err := e.dispatch(context.Background(), json.RawMessage(`{"action":"list_programs"}`))
	if err != nil || !res.OK {
		t.Fatalf("list: %+v %v", res, err)
	}
	var catalog map[string]struct {
		Description, Instructions string
		Configured                bool
	}
	if err := json.Unmarshal(res.Data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 || !catalog["records"].Configured || catalog["viewer"].Configured || catalog["records"].Instructions != "Use for patient lookups" {
		t.Fatalf("catalog: %s", res.Data)
	}
	if strings.Contains(string(res.Data), "Private") || strings.Contains(string(res.Data), "billing") {
		t.Fatalf("non-project installation exposed: %s", res.Data)
	}
}
