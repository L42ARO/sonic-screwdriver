package routes

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

var nameRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_\-]{0,63}$`)

// syncOperations makes a project's operations exactly the posted list.
func (d Deps) syncOperations(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if !nameRE.MatchString(project) {
		utils.WriteError(w, utils.NewError(http.StatusBadRequest, "invalid_definition", "invalid project name "+project))
		return
	}
	var defs []store.Definition
	if err := utils.ReadJSON(r, &defs); err != nil {
		utils.WriteError(w, err)
		return
	}
	if err := checkDefinitions(defs); err != nil {
		utils.WriteError(w, utils.NewError(http.StatusBadRequest, "invalid_definition", err.Error()))
		return
	}
	res, err := d.Store.SyncOperations(r.Context(), d.Store.UserID, project, defs)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, res)
}

func (d Deps) listOperations(w http.ResponseWriter, r *http.Request) {
	ops, err := d.Store.ListOperations(r.Context(), d.Store.UserID, r.PathValue("project"))
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"operations": ops})
}

// checkDefinitions validates every definition before anything is stored.
func checkDefinitions(defs []store.Definition) error {
	seen := map[string]bool{}
	for i, d := range defs {
		where := fmt.Sprintf("operation %d (%s)", i, d.Name)
		switch {
		case !nameRE.MatchString(d.Name):
			return fmt.Errorf("%s: name must be letters, digits, _ or -", where)
		case seen[d.Name]:
			return fmt.Errorf("%s: name is used twice", where)
		case strings.TrimSpace(d.Description) == "":
			return fmt.Errorf("%s: description is empty", where)
		case strings.TrimSpace(d.Instructions) == "":
			return fmt.Errorf("%s: instructions are empty", where)
		case len(d.Input) == 0 || len(d.Output) == 0:
			return fmt.Errorf("%s: input and output schemas are required", where)
		}
		seen[d.Name] = true
		if _, err := utils.CompileSchema("input", d.Input); err != nil {
			return fmt.Errorf("%s: %v", where, err)
		}
		if t := utils.SchemaType(d.Input); t != "object" {
			return fmt.Errorf("%s: input schema must be an object, not %q", where, t)
		}
		if _, err := utils.CompileSchema("output", d.Output); err != nil {
			return fmt.Errorf("%s: %v", where, err)
		}
	}
	return nil
}
