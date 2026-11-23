package routes

import (
	"net/http"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/hub"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

func (d Deps) listMachines(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	machines, err := d.Store.ListMachines(r.Context(), user)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(machines))
	for _, m := range machines {
		key := hub.Machine{User: user, Name: m.Name}
		out = append(out, map[string]any{
			"name":      m.Name,
			"online":    d.Hub.Online(key),
			"last_seen": m.LastSeen,
			"queued":    len(d.Jobs.QueuedFor(key)),
		})
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"machines": out})
}

func (d Deps) usage(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	total, err := d.Store.UserSpend(r.Context(), user)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	spent, capUSD, err := d.Ledger.Budget(r.Context(), user)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"total": total, "budget_usd": capUSD, "counted_against_budget_usd": spent})
}
