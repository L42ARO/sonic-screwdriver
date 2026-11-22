package routes

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/services/jobs"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// createJob queues an operation run. With Accept: text/event-stream it streams the job
// until it ends; otherwise it returns the job id at once.
func (d Deps) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Machine string          `json:"machine"`
		Input   json.RawMessage `json:"input"`
	}
	if err := utils.ReadJSON(r, &body); err != nil {
		utils.WriteError(w, err)
		return
	}
	job, err := d.Jobs.Submit(r.Context(), jobs.Submission{
		Project:        r.PathValue("project"),
		Operation:      r.PathValue("name"),
		Machine:        body.Machine,
		Input:          body.Input,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	if wantsStream(r) {
		d.stream(w, r, job.ID)
		return
	}
	utils.WriteJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID, "status": job.Status})
}

func (d Deps) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := d.Store.GetJob(r.Context(), d.Store.UserID, r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	if j == nil {
		utils.WriteError(w, utils.NewError(http.StatusNotFound, "not_found", "no job "+r.PathValue("id")))
		return
	}
	out := map[string]any{"job": j}
	if j.ErrorCode != "" {
		out["error"] = map[string]string{"code": j.ErrorCode, "message": j.ErrorMessage}
	}
	utils.WriteJSON(w, http.StatusOK, out)
}

// jobEvents streams a job from the start, for clients reconnecting after a drop.
func (d Deps) jobEvents(w http.ResponseWriter, r *http.Request) {
	d.stream(w, r, r.PathValue("id"))
}

func (d Deps) stream(w http.ResponseWriter, r *http.Request, jobID string) {
	history, live, cancel, err := d.Jobs.Subscribe(r.Context(), jobID)
	if err != nil {
		utils.WriteError(w, err)
		return
	}
	defer cancel()
	sse := utils.NewSSE(w)
	if sse == nil {
		utils.WriteError(w, utils.NewError(http.StatusInternalServerError, "internal", "streaming unsupported"))
		return
	}
	for _, e := range history {
		if sse.Event(e.Name, e.Data) != nil {
			return
		}
	}
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case e, ok := <-live:
			if !ok {
				return
			}
			if sse.Event(e.Name, e.Data) != nil {
				return
			}
		case <-keepAlive.C:
			if sse.KeepAlive() != nil {
				return
			}
		case <-r.Context().Done():
			return // the caller left; the job keeps running
		}
	}
}

func wantsStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
