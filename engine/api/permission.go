package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-gorp/gorp"
	"github.com/gorilla/mux"

	"github.com/ovh/cds/engine/api/auth"
	"github.com/ovh/cds/engine/api/group"
	"github.com/ovh/cds/engine/api/permission"
	"github.com/ovh/cds/engine/api/worker"
	"github.com/ovh/cds/engine/api/workflow"
	"github.com/ovh/cds/sdk"
	"github.com/ovh/cds/sdk/log"
)

// PermCheckFunc defines func call to check permission
type PermCheckFunc func(ctx context.Context, key string, permission int, routeVar map[string]string) bool

func permissionFunc(api *API) map[string]PermCheckFunc {
	return map[string]PermCheckFunc{
		"permProjectKey":      api.checkProjectPermissions,
		"permPipelineKey":     api.checkPipelinePermissions,
		"permApplicationName": api.checkApplicationPermissions,
		"appID":               api.checkApplicationIDPermissions,
		"permGroupName":       api.checkGroupPermissions,
		"permActionName":      api.checkActionPermissions,
		"permEnvironmentName": api.checkEnvironmentPermissions,
		"permModelID":         api.checkWorkerModelPermissions,
	}
}

func getPermissionByMethod(method string, isExecution bool) int {
	switch method {
	case "POST":
		if isExecution {
			return permission.PermissionReadExecute
		}
		return permission.PermissionReadWriteExecute
	case "PUT":
		return permission.PermissionReadWriteExecute
	case "DELETE":
		return permission.PermissionReadWriteExecute
	default:
		return permission.PermissionRead
	}
}

func (api *API) DeletePermissionMiddleware(ctx context.Context, w http.ResponseWriter, req *http.Request, rc *HandlerConfig) (context.Context, error) {
	if req.Method == "POST" || req.Method == "PUT" || req.Method == "DELETE" {
		deleteUserPermissionCache(ctx)
	}
	return ctx, nil
}

func (api *API) AuthMiddleware(ctx context.Context, w http.ResponseWriter, req *http.Request, rc *HandlerConfig) (context.Context, error) {
	headers := req.Header

	if rc.Options["auth"] == "true" {
		switch headers.Get("User-Agent") {
		case sdk.HatcheryAgent:
			var err error
			ctx, err = auth.CheckHatcheryAuth(ctx, api.MustDB(), headers)
			if err != nil {
				return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Authorization denied on %s %s for %s agent %s : %s", req.Method, req.URL, req.RemoteAddr, getAgent(req), err)
			}
		case sdk.WorkerAgent:
			var err error
			ctx, err = auth.CheckWorkerAuth(ctx, api.MustDB(), headers)
			if err != nil {
				return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Authorization denied on %s %s for %s agent %s : %s", req.Method, req.URL, req.RemoteAddr, getAgent(req), err)
			}
		default:
			var err error
			ctx, err = api.Router.AuthDriver.CheckAuth(ctx, w, req)
			if err != nil {
				return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Authorization denied on %s %s for %s agent %s : %s", req.Method, req.URL, req.RemoteAddr, getAgent(req), err)
			}
		}
	}

	if getUser(ctx) != nil {
		if err := loadUserPermissions(api.MustDB(), getUser(ctx)); err != nil {
			return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Unable to load user %s permission: %s", getUser(ctx).ID, err)
		}
	}

	if getHatchery(ctx) != nil {
		g, err := loadGroupPermissions(api.MustDB(), getHatchery(ctx).GroupID)
		if err != nil {
			return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> cannot load group permissions for GroupID %d err:%s", getHatchery(ctx).GroupID, err)
		}
		getUser(ctx).Groups = append(getUser(ctx).Groups, *g)
	}

	if getWorker(ctx) != nil {
		if err := worker.RefreshWorker(api.MustDB(), getWorker(ctx).ID); err != nil {
			return ctx, sdk.WrapError(err, "Router> Unable to refresh worker")
		}

		g, err := loadGroupPermissions(api.MustDB(), getWorker(ctx).GroupID)
		if err != nil {
			return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> cannot load group permissions: %s", err)
		}
		getUser(ctx).Groups = append(getUser(ctx).Groups, *g)

		if getWorker(ctx).ModelID != 0 {
			//Load model
			m, err := worker.LoadWorkerModelByID(api.MustDB(), getWorker(ctx).ModelID)
			if err != nil {
				return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> cannot load worker: %s", err)
			}

			//If worker model is owned by shared.infra, let's add SharedInfraGroup in user's group
			if m.GroupID == group.SharedInfraGroup.ID {
				getUser(ctx).Groups = append(getUser(ctx).Groups, *group.SharedInfraGroup)
			} else {
				log.Debug("Router> loading groups permission for model %d", getWorker(ctx).ModelID)
				modelGroup, errLoad2 := loadGroupPermissions(api.MustDB(), m.GroupID)
				if errLoad2 != nil {
					return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Cannot load group: %s", errLoad2)
				}
				//Anyway, add the group of the model as a group of the user
				getUser(ctx).Groups = append(getUser(ctx).Groups, *modelGroup)
			}
		}
	}

	if rc.Options["auth"] != "true" {
		return ctx, nil
	} else {
		if getUser(ctx) == nil {
			return ctx, sdk.WrapError(sdk.ErrUnauthorized, "Router> Unable to find connected user")
		}

		if rc.Options["needHatchery"] == "true" && getHatchery(ctx) != nil {
			return ctx, nil
		}

		if rc.Options["needWorker"] == "true" {
			permissionOk := api.checkWorkerPermission(ctx, api.MustDB(), rc, mux.Vars(req))
			if !permissionOk {
				return ctx, sdk.WrapError(sdk.ErrForbidden, "Router> Worker not authorized")
			}
			return ctx, nil
		}

		if getUser(ctx).Admin {
			return ctx, nil
		}

		if rc.Options["needAdmin"] != "true" {
			permissionOk := api.checkPermission(ctx, mux.Vars(req), getPermissionByMethod(req.Method, rc.Options["isExecution"] == "true"))
			if !permissionOk {
				return ctx, sdk.WrapError(sdk.ErrForbidden, "Router> User not authorized")
			}
		} else {
			return ctx, sdk.WrapError(sdk.ErrForbidden, "Router> User not authorized")
		}

		if rc.Options["needUsernameOrAdmin"] == "true" && getUser(ctx).Username != mux.Vars(req)["username"] {
			// get / update / delete user -> for admin or current user
			// if not admin and currentUser != username in request -> ko
			return ctx, sdk.WrapError(sdk.ErrForbidden, "Router> User not authorized on this resource")
		}

		return ctx, nil
	}
}

func (api *API) checkWorkerPermission(ctx context.Context, db gorp.SqlExecutor, rc *HandlerConfig, routeVar map[string]string) bool {
	if getWorker(ctx) == nil {
		return false
	}

	idS, ok := routeVar["permID"]
	if !ok {
		return true
	}

	id, err := strconv.ParseInt(idS, 10, 64)
	if err != nil {
		log.Error("checkWorkerPermission> Unable to parse permID=%s", idS)
		return false
	}

	//IF it is POSTEXECUTE, it means that the job is must be taken by the worker
	if rc.Options["isExecution"] == "true" {
		node, err := workflow.LoadNodeJobRun(db, id)
		if err != nil {
			log.Error("checkWorkerPermission> Unable to load job %d", id)
			return false
		}
		return node.Job.WorkerName == getWorker(ctx).Name && node.Job.WorkerID == getWorker(ctx).ID
	}
	return true
}

func (api *API) checkPermission(ctx context.Context, routeVar map[string]string, permission int) bool {
	log.Debug("checkPermission : %+v", getUser(ctx))
	for _, g := range getUser(ctx).Groups {
		if group.SharedInfraGroup != nil && g.Name == group.SharedInfraGroup.Name {
			return true
		}
	}

	permissionOk := true
	for key, value := range routeVar {
		if permFunc, ok := permissionFunc(api)[key]; ok {
			log.Debug("Check permission for %s", key)
			permissionOk = permFunc(ctx, value, permission, routeVar)
			if !permissionOk {
				return permissionOk
			}
		}
	}
	return permissionOk
}

func (api *API) checkProjectPermissions(ctx context.Context, projectKey string, permission int, routeVar map[string]string) bool {
	if getUser(ctx).Groups != nil {
		for _, g := range getUser(ctx).Groups {
			for _, p := range g.ProjectGroups {
				if projectKey == p.Project.Key && p.Permission >= permission {
					return true
				}
			}
		}
	}
	log.Warning("Access denied. user %s on project %s", getUser(ctx).Username, projectKey)
	return false
}

func (api *API) checkPipelinePermissions(ctx context.Context, pipelineName string, permission int, routeVar map[string]string) bool {
	// Check if param key exist
	if projectKey, ok := routeVar["key"]; ok {
		for _, g := range getUser(ctx).Groups {
			for _, p := range g.PipelineGroups {
				if pipelineName == p.Pipeline.Name && p.Permission >= permission && projectKey == p.Pipeline.ProjectKey {
					return true
				}
			}
		}
		log.Warning("Access denied. user %s on pipeline %s", getUser(ctx).Username, pipelineName)
	} else {
		log.Warning("Wrong route configuration. need key parameter")
	}
	return false
}

func (api *API) checkEnvironmentPermissions(ctx context.Context, envName string, permission int, routeVar map[string]string) bool {
	// Check if param key exist
	if projectKey, ok := routeVar["key"]; ok {
		if getUser(ctx).Groups != nil {
			for _, g := range getUser(ctx).Groups {
				for _, p := range g.EnvironmentGroups {
					if envName == p.Environment.Name && p.Permission >= permission && projectKey == p.Environment.ProjectKey {
						return true
					}
				}
			}
		}
		log.Warning("Access denied. user %s on environment %s", getUser(ctx).Username, envName)
	} else {
		log.Warning("Wrong route configuration. need key parameter")
	}
	return false
}

func (api *API) checkApplicationPermissions(ctx context.Context, applicationName string, permission int, routeVar map[string]string) bool {
	// Check if param key exist
	if projectKey, ok := routeVar["key"]; ok {
		if getUser(ctx).Groups != nil {
			for _, g := range getUser(ctx).Groups {
				for _, a := range g.ApplicationGroups {
					if applicationName == a.Application.Name && a.Permission >= permission && projectKey == a.Application.ProjectKey {
						return true
					}
				}
			}
		}
		log.Warning("Access denied. user %s on application %s", getUser(ctx).Username, applicationName)
	} else {
		log.Warning("Wrong route configuration. need key parameter")
	}
	return false
}

func (api *API) checkApplicationIDPermissions(ctx context.Context, appIDS string, permission int, routeVar map[string]string) bool {
	appID, err := strconv.ParseInt(appIDS, 10, 64)
	if err != nil {
		log.Warning("checkApplicationIDPermissions> appID (%s) is not an integer: %s", appIDS, err)
		return false
	}

	if getUser(ctx).Groups != nil {
		for _, g := range getUser(ctx).Groups {
			for _, a := range g.ApplicationGroups {
				if appID == a.Application.ID && a.Permission >= permission {
					return true
				}
			}
		}
	}

	log.Warning("Access denied. user %s on application %s", getUser(ctx).Username, appIDS)
	return false
}

func (api *API) checkGroupPermissions(ctx context.Context, groupName string, permissionValue int, routeVar map[string]string) bool {
	for _, g := range getUser(ctx).Groups {
		if g.Name == groupName {

			if permissionValue == permission.PermissionRead {
				return true
			}

			for i := range g.Admins {
				if g.Admins[i].ID == getUser(ctx).ID {
					return true
				}
			}
		}
	}

	return false
}

func (api *API) checkActionPermissions(ctx context.Context, groupName string, permissionValue int, routeVar map[string]string) bool {
	if permissionValue == permission.PermissionRead {
		return true
	}

	if permissionValue != permission.PermissionRead && getUser(ctx).Admin {
		return true
	}

	return false
}

func (api *API) checkWorkerModelPermissions(ctx context.Context, modelID string, permissionValue int, routeVar map[string]string) bool {
	id, err := strconv.ParseInt(modelID, 10, 64)
	if err != nil {
		log.Warning("checkWorkerModelPermissions> modelID is not an integer: %s", err)
		return false
	}

	m, err := worker.LoadWorkerModelByID(api.MustDB(), id)
	if err != nil {
		log.Warning("checkWorkerModelPermissions> unable to load model by id %s: %s", modelID, err)
		return false
	}

	if getHatchery(ctx) != nil {
		return getHatchery(ctx).GroupID == group.SharedInfraGroup.ID || m.GroupID == getHatchery(ctx).GroupID
	}
	return api.checkWorkerModelPermissionsByUser(m, getUser(ctx), permissionValue)
}

func (api *API) checkWorkerModelPermissionsByUser(m *sdk.Model, u *sdk.User, permissionValue int) bool {
	if u.Admin {
		return true
	}

	for _, g := range u.Groups {
		if g.ID == m.GroupID {
			for _, a := range g.Admins {
				if a.ID == u.ID {
					return true
				}
			}

			if permissionValue == permission.PermissionRead {
				return true
			}
		}
	}
	return false
}
