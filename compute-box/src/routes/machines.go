package routes

import (
	"net/http"

	"sonic-screwdriver/compute-box/src/utils"
)

func (d Deps) listMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := d.Store.ListMachines(r.Context(), d.Store.UserID)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(machines))
	for _, m := range machines {
		out = append(out, map[string]any{
			"name":      m.Name,
			"online":    d.Hub.Online(m.Name),
			"last_seen": m.LastSeen,
			"queued":    len(d.Jobs.QueuedFor(m.Name)),
		})
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"machines": out})
}

func (d Deps) usage(w http.ResponseWriter, r *http.Request) {
	total, err := d.Store.TotalSpend(r.Context())
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	spent, capUSD := d.Ledger.Spent()
	utils.WriteJSON(w, http.StatusOK, map[string]any{"total": total, "budget_usd": capUSD, "counted_against_budget_usd": spent})
}
