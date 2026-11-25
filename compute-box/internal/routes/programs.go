package routes

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/store"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

func (d Deps) projectPrograms(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	if !nameRE.MatchString(name) {
		programError(w, "invalid project name")
		return
	}
	if r.Method == http.MethodPut {
		var programs map[string]store.Program
		if err := utils.ReadJSON(r, &programs); err != nil {
			utils.WriteError(w, err)
			return
		}
		if programs == nil {
			programError(w, "programs must be an object; use {} to clear explicitly")
			return
		}
		for id, p := range programs {
			if !nameRE.MatchString(id) || strings.TrimSpace(p.Description) == "" {
				programError(w, "each program needs a valid ID and nonempty description")
				return
			}
		}
		if err := d.Store.SetProjectPrograms(r.Context(), userOf(r), name, programs); err != nil {
			utils.WriteError(w, err)
			return
		}
	}
	programs, err := d.Store.ProjectPrograms(r.Context(), userOf(r), name)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, programs)
}

func (d Deps) machinePrograms(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	if !nameRE.MatchString(name) {
		programError(w, "invalid machine name")
		return
	}
	if r.Method == http.MethodPut {
		var programs map[string]store.InstalledProgram
		if err := utils.ReadJSON(r, &programs); err != nil {
			utils.WriteError(w, err)
			return
		}
		if programs == nil {
			programError(w, "programs must be an object; use {} to clear explicitly")
			return
		}
		for id, p := range programs {
			if !nameRE.MatchString(id) || !absoluteWindowsPath(p.Executable) || strings.TrimSpace(p.WindowTitleContains) == "" ||
				(p.WorkingDirectory != "" && !absoluteWindowsPath(p.WorkingDirectory)) || p.StartupTimeoutMS < 0 || p.StartupTimeoutMS > 120000 {
				programError(w, fmt.Sprintf(`%s: require an absolute Windows executable path (C:\..., \\server\share\..., or starting with a variable like %%SONIC_AGENT_DIR%%\...), window_title_contains, optional absolute working_directory, and startup_timeout_ms from 0 to 120000 (0 defaults to 30000)`, id))
				return
			}
			if strings.ContainsRune(p.Executable+p.WorkingDirectory+p.WindowTitleContains, 0) {
				programError(w, "NUL is not allowed")
				return
			}
			for _, arg := range p.Args {
				if strings.ContainsRune(arg, 0) {
					programError(w, "NUL is not allowed in arguments")
					return
				}
			}
		}
		if err := d.Store.SetMachinePrograms(r.Context(), userOf(r), name, programs); err != nil {
			utils.WriteError(w, err)
			return
		}
	}
	programs, err := d.Store.MachinePrograms(r.Context(), userOf(r), name)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, programs)
}

// The server may run on Linux while the executor is Windows. A path may also start with
// an environment variable (%LOCALAPPDATA%\..., %SONIC_AGENT_DIR%\...) that the local-agent
// expands on its own machine, so one configuration works on every computer.
func absoluteWindowsPath(path string) bool {
	drive := len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
	unc := strings.HasPrefix(path, `\\`) && len(strings.Split(strings.Trim(path, `\`), `\`)) >= 3
	return drive || unc || variablePathRE.MatchString(path)
}

var variablePathRE = regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_()]*%[\\/].`)

func programError(w http.ResponseWriter, message string) {
	utils.WriteError(w, utils.NewError(http.StatusBadRequest, "invalid_definition", message))
}
