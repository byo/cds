package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/ovh/cds/engine/api/hatchery"
	"github.com/ovh/cds/engine/api/worker"
	"github.com/ovh/cds/sdk"
	"github.com/ovh/cds/sdk/log"
)

func (api *API) registerWorkerHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		params := &worker.RegistrationForm{}
		if err := UnmarshalBody(r, params); err != nil {
			return sdk.WrapError(err, "registerWorkerHandler> Unable to parse registration form")
		}

		// Check that hatchery exists
		var h *sdk.Hatchery
		if params.HatcheryName != "" {
			var errH error
			h, errH = hatchery.LoadHatcheryByName(api.MustDB(), params.HatcheryName)
			if errH != nil {
				return sdk.WrapError(errH, "registerWorkerHandler> Unable to load hatchery %s", params.HatcheryName)
			}
		}

		// Try to register worker
		worker, err := worker.RegisterWorker(api.MustDB(), params.Name, params.Token, params.ModelID, h, params.BinaryCapabilities)
		if err != nil {
			err = sdk.NewError(sdk.ErrUnauthorized, err)
			return sdk.WrapError(err, "registerWorkerHandler> [%s] Registering failed", params.Name)
		}

		worker.Uptodate = params.Version == sdk.VERSION

		log.Debug("New worker: [%s] - %s", worker.ID, worker.Name)

		// Return worker info to worker itself
		return WriteJSON(w, r, worker, http.StatusOK)
	}
}

func (api *API) getWorkersHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return sdk.WrapError(err, "getWorkerModels> cannot parse form")
		}

		workers, errl := worker.LoadWorkers(api.MustDB())
		if errl != nil {
			return sdk.WrapError(errl, "getWorkerModels> cannot load workers")
		}

		return WriteJSON(w, r, workers, http.StatusOK)
	}
}

func (api *API) disableWorkerHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		// Get pipeline and action name in URL
		vars := mux.Vars(r)
		id := vars["id"]

		tx, err := api.MustDB().Begin()
		if err != nil {
			return sdk.WrapError(err, "disabledWorkerHandler> Cannot start tx")
		}
		defer tx.Rollback()

		wor, err := worker.LoadWorker(tx, id)
		if err != nil {
			if err != sql.ErrNoRows {
				return sdk.WrapError(err, "disabledWorkerHandler> Cannot load worker %s", id)
			}
			return sdk.WrapError(sdk.ErrNotFound, "disabledWorkerHandler> Cannot load worker %s", id)
		}

		if wor.Status == sdk.StatusBuilding {
			return sdk.WrapError(sdk.ErrForbidden, "Cannot disable a worker with status %s\n", wor.Status)
		}

		if wor.Status == sdk.StatusChecking {
			log.Warning("disableWorkerHandler> Next time, we will see (%s) %s at status waiting, we will kill it\n", wor.ID, wor.Name)
			go func(w *sdk.Worker) {
				for {
					var attempts int
					time.Sleep(500 * time.Millisecond)
					db := api.DBConnectionFactory.GetDBMap()
					if db != nil {
						attempts++
						w1, err := worker.LoadWorker(api.MustDB(), w.ID)
						if err != nil {
							log.Warning("disableWorkerHandler> Error getting worker %s", w.ID)
							return
						}
						//Give up is worker is building
						if w1.Status == sdk.StatusBuilding {
							return
						}
						if w1.Status == sdk.StatusWaiting {
							if err := worker.UpdateWorkerStatus(tx, id, sdk.StatusDisabled); err != nil {
								log.Warning("disableWorkerHandler> Error disabling worker %s", w.ID)
								return
							}
						}
						if attempts > 100 {
							log.Error("disableWorkerHandler> Unable to disabled worker %s %s", w.ID, w.Name)
							return
						}
					}
				}
			}(wor)
		}

		if wor.HatcheryID == 0 {
			return sdk.WrapError(sdk.ErrForbidden, "disableWorkerHandler> Cannot disable a worker (%s) not started by an hatchery", wor.Name)
		}

		if err := worker.UpdateWorkerStatus(tx, id, sdk.StatusDisabled); err != nil {
			if err == worker.ErrNoWorker || err == sql.ErrNoRows {
				return sdk.WrapError(sdk.ErrWrongRequest, "disableWorkerHandler> handler %s does not exists", id)
			}
			return sdk.WrapError(err, "disableWorkerHandler> cannot update worker status")
		}

		if err := tx.Commit(); err != nil {
			return sdk.WrapError(err, "disableWorkerHandler> cannot commit tx")
		}

		return nil
	}
}

func (api *API) refreshWorkerHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		if err := worker.RefreshWorker(api.MustDB(), getWorker(ctx).ID); err != nil && (err != sql.ErrNoRows || err != worker.ErrNoWorker) {
			return sdk.WrapError(err, "refreshWorkerHandler> cannot refresh last beat of %s", getWorker(ctx).ID)
		}
		return nil
	}
}

func (api *API) unregisterWorkerHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		if err := worker.DeleteWorker(api.MustDB(), getWorker(ctx).ID); err != nil {
			return sdk.WrapError(err, "unregisterWorkerHandler> cannot delete worker %s", getWorker(ctx).ID)
		}
		return nil
	}
}

func (api *API) workerCheckingHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		wk, errW := worker.LoadWorker(api.MustDB(), getWorker(ctx).ID)
		if errW != nil {
			return sdk.WrapError(errW, "workerCheckingHandler> Unable to load worker %s", getWorker(ctx).ID)
		}

		if wk.Status != sdk.StatusWaiting {
			log.Debug("workerCheckingHandler> Worker %s cannot be Checking. Current status: %s", wk.Name, wk.Status)
			return nil
		}

		if err := worker.SetStatus(api.MustDB(), getWorker(ctx).ID, sdk.StatusChecking); err != nil {
			return sdk.WrapError(err, "workerCheckingHandler> cannot update worker %s", getWorker(ctx).ID)
		}

		return nil
	}
}

func (api *API) workerWaitingHandler() Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		wk, errW := worker.LoadWorker(api.MustDB(), getWorker(ctx).ID)
		if errW != nil {
			return sdk.WrapError(errW, "workerWaitingHandler> Unable to load worker %s", getWorker(ctx).ID)
		}

		if wk.Status == sdk.StatusWaiting {
			return nil
		}

		if wk.Status != sdk.StatusChecking && wk.Status != sdk.StatusBuilding {
			log.Debug("workerWaitingHandler> Worker %s cannot be Waiting. Current status: %s", wk.Name, wk.Status)
			return nil
		}

		if err := worker.SetStatus(api.MustDB(), getWorker(ctx).ID, sdk.StatusWaiting); err != nil {
			return sdk.WrapError(err, "workerWaitingHandler> cannot update worker %s", getWorker(ctx).ID)
		}

		return nil
	}
}
