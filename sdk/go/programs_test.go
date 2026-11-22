package sonic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProgramSync(t *testing.T) {
	var paths []string
	var bodies []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]json.RawMessage
		if r.URL.Path != "/v1/projects/clinic/operations" {
			json.NewDecoder(r.Body).Decode(&body)
		}
		bodies = append(bodies, body)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	machine := NewMachine("desk", map[string]InstalledProgram{"records": {Executable: `C:\Apps\records.exe`, Args: []string{"a b", "x&y"}, WindowTitleContains: "Records"}}, WithURL(server.URL))
	project := NewProject("clinic", WithURL(server.URL), WithMachine(machine.Name), WithPrograms(map[string]Program{"records": {Description: "Patient records"}}))
	project.ops = []Definition{{Name: "lookup"}}
	if err := machine.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := project.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0] != "/v1/machines/desk/programs" || paths[1] != "/v1/projects/clinic/programs" || paths[2] != "/v1/projects/clinic/operations" {
		t.Fatalf("wrong paths: %v", paths)
	}
	var installation InstalledProgram
	json.Unmarshal(bodies[0]["records"], &installation)
	if len(installation.Args) != 2 || installation.Args[0] != "a b" || installation.Args[1] != "x&y" {
		t.Fatalf("arguments lost: %+v", installation)
	}
	var guidance map[string]any
	json.Unmarshal(bodies[1]["records"], &guidance)
	if _, ok := guidance["executable"]; ok {
		t.Fatal("installation leaked into project guidance")
	}
	paths = nil
	project.Programs = nil
	if err := project.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatal("legacy sync overwrote program config")
	}
}
