package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/file"
	"github.com/anilyagiz/automdm/v4/pkg/scripts"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
)

////////////////////////////////////////////////////////////////////////////////
// Run Script on a Host (async)
////////////////////////////////////////////////////////////////////////////////

func runScriptEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.RunScriptRequest)

	var noWait time.Duration
	result, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{
		HostID:         req.HostID,
		ScriptID:       req.ScriptID,
		ScriptContents: req.ScriptContents,
		ScriptName:     req.ScriptName,
		TeamID:         req.TeamID,
	}, noWait)
	if err != nil {
		return automdm.RunScriptResponse{Err: err}, nil
	}
	return automdm.RunScriptResponse{HostID: result.HostID, ExecutionID: result.ExecutionID}, nil
}

////////////////////////////////////////////////////////////////////////////////
// Run Script on a Host (sync)
////////////////////////////////////////////////////////////////////////////////

// this is to be used only by tests, to be able to use a shorter timeout.
var testRunScriptWaitForResult time.Duration

func runScriptSyncEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	waitForResult := scripts.MaxServerWaitTime
	if testRunScriptWaitForResult != 0 {
		waitForResult = testRunScriptWaitForResult
	}

	req := request.(*automdm.RunScriptSyncRequest)
	result, err := svc.RunHostScript(ctx, &automdm.HostScriptRequestPayload{
		HostID:         req.HostID,
		ScriptID:       req.ScriptID,
		ScriptContents: req.ScriptContents,
		ScriptName:     req.ScriptName,
		TeamID:         req.TeamID,
	}, waitForResult)
	var hostTimeout bool
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return automdm.RunScriptSyncResponse{Err: err}, nil
		}
		// We should still return the execution id and host id in this timeout case,
		// so the user knows what script request to look at in the UI. We cannot
		// return an error (field Err) in this case, as the Errorer interface's
		// rendering logic would take over and only render the error part of the
		// response struct.
		hostTimeout = true
	}
	result.Message = result.UserMessage(hostTimeout, result.Timeout)
	return automdm.RunScriptSyncResponse{
		HostScriptResult: result,
		HostTimeout:      hostTimeout,
	}, nil
}

func (svc *Service) GetScriptIDByName(ctx context.Context, scriptName string, teamID *uint) (uint, error) {
	// TODO: confirm auth level
	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: teamID}, automdm.ActionRead); err != nil {
		return 0, err
	}

	id, err := svc.ds.GetScriptIDByName(ctx, scriptName, teamID)
	if err != nil {
		if automdm.IsNotFound(err) {
			return 0, automdm.NewInvalidArgumentError("script_name", fmt.Sprintf(`Script '%s' doesn’t exist.`, scriptName))
		}
		return 0, err
	}
	return id, nil
}

const maxPendingScripts = 1000

func (svc *Service) RunHostScript(ctx context.Context, request *automdm.HostScriptRequestPayload, waitForResult time.Duration) (*automdm.HostScriptResult, error) {
	// Must check for presence of mutually exclusive parameters before
	// authorization, as the permissions are not the same in all cases.
	// There's no harm in returning the error if this validation fails,
	// since all values are user-provided it doesn't leak any internal
	// information.
	if err := request.ValidateParams(waitForResult); err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, err
	}

	if request.TeamID > 0 {
		lic, _ := license.FromContext(ctx)
		if !lic.IsPremium() {
			svc.authz.SkipAuthorization(ctx)
			return nil, automdm.ErrMissingLicense
		}
	}

	if request.ScriptContents != "" {
		if err := automdm.ValidateEmbeddedSecretsAndCustomHostVitals(ctx, svc.ds, []string{request.ScriptContents}); err != nil {
			svc.authz.SkipAuthorization(ctx)
			return nil, automdm.NewInvalidArgumentError("script", err.Error())
		}
		if err := automdm.ValidateAutoMDMVariablesInScript(request.ScriptContents, license.IsPremium(ctx)); err != nil {
			svc.authz.SkipAuthorization(ctx)
			return nil, err
		}
	}

	if request.ScriptName != "" {
		scriptID, err := svc.GetScriptIDByName(ctx, request.ScriptName, &request.TeamID)
		if err != nil {
			return nil, err
		}
		request.ScriptID = &scriptID
	}

	// must load the host to get the team (cannot use lite, the last seen time is
	// required to check if it is online) to authorize with the proper team id.
	// We cannot first authorize if the user can list hosts, in case we
	// eventually allow a write-only role (e.g. gitops).
	host, err := svc.ds.Host(ctx, request.HostID)
	if err != nil {
		// if error is because the host does not exist, check first if the user
		// had access to run a script (to prevent leaking valid host ids).
		if automdm.IsNotFound(err) {
			if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{ScriptID: request.ScriptID}, automdm.ActionWrite); err != nil {
				return nil, err
			}
		}
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "get host lite")
	}

	if host.OrbitNodeKey == nil || *host.OrbitNodeKey == "" {
		// automdmd is required to run scripts so if the host is enrolled via plain osquery we return
		// an error
		svc.authz.SkipAuthorization(ctx)
		return nil, automdm.NewUserMessageError(errors.New(automdm.RunScriptDisabledErrMsg), http.StatusUnprocessableEntity)
	}

	// If scripts are disabled (according to the last detail query), we return an error.
	// host.ScriptsEnabled may be nil for older orbit versions.
	if host.ScriptsEnabled != nil && !*host.ScriptsEnabled {
		svc.authz.SkipAuthorization(ctx)
		return nil, automdm.NewUserMessageError(errors.New(automdm.RunScriptsOrbitDisabledErrMsg), http.StatusUnprocessableEntity)
	}

	maxPending := maxPendingScripts

	// authorize with the host's team; script_id matters because some roles can
	// only run saved scripts, not ad hoc ones.
	if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{TeamID: host.TeamID, ScriptID: request.ScriptID}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	var isSavedScript bool
	if request.ScriptID != nil {
		script, err := svc.ds.Script(ctx, *request.ScriptID)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil, automdm.NewInvalidArgumentError("script_id", `No script exists for the provided "script_id".`).
					WithStatus(http.StatusNotFound)
			}
			return nil, err
		}
		var scriptTmID, hostTmID uint
		if script.TeamID != nil {
			scriptTmID = *script.TeamID
		}
		if host.TeamID != nil {
			hostTmID = *host.TeamID
		}
		if scriptTmID != hostTmID {
			return nil, automdm.NewInvalidArgumentError("script_id", `The script does not belong to the same team (or "Unassigned") as the host.`)
		}

		isQueued, err := svc.ds.IsExecutionPendingForHost(ctx, request.HostID, *request.ScriptID)
		if err != nil {
			return nil, err
		}

		if isQueued {
			return nil, automdm.NewInvalidArgumentError("script_id", `The script is already queued on the given host.`).WithStatus(http.StatusConflict)
		}

		contents, err := svc.ds.GetScriptContents(ctx, *request.ScriptID)
		if err != nil {
			if automdm.IsNotFound(err) {
				return nil, automdm.NewInvalidArgumentError("script_id", `No script exists for the provided "script_id".`).
					WithStatus(http.StatusNotFound)
			}
			return nil, err
		}
		request.ScriptContents = string(contents)
		request.ScriptContentID = script.ScriptContentID
		isSavedScript = true
	}

	if err := automdm.ValidateHostScriptContents(request.ScriptContents, isSavedScript); err != nil {
		return nil, automdm.NewInvalidArgumentError("script_contents", err.Error())
	}

	asyncExecution := waitForResult <= 0

	if !asyncExecution && host.Status(time.Now()) != automdm.StatusOnline {
		return nil, automdm.NewInvalidArgumentError("host_id", automdm.RunScriptHostOfflineErrMsg)
	}

	pending, err := svc.ds.ListPendingHostScriptExecutions(ctx, request.HostID)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "list host pending script executions")
	}
	if len(pending) >= maxPending {
		return nil, automdm.NewInvalidArgumentError(
			"script_id", "cannot queue more than 1000 scripts per host",
		).WithStatus(http.StatusConflict)
	}

	if !asyncExecution && len(pending) > 0 {
		return nil, automdm.NewInvalidArgumentError("script_id", automdm.RunScriptAlreadyRunningErrMsg).WithStatus(http.StatusConflict)
	}

	// create the script execution request, the host will be notified of the
	// script execution request via the orbit config's Notifications mechanism.
	if ctxUser := authz.UserFromContext(ctx); ctxUser != nil {
		request.UserID = &ctxUser.ID
	}
	request.SyncRequest = !asyncExecution
	script, err := svc.ds.NewHostScriptExecutionRequest(ctx, request)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "create script execution request")
	}
	script.Hostname = host.DisplayName()

	if asyncExecution {
		// async execution, return
		return script, nil
	}

	ctx, cancel := context.WithTimeout(ctx, waitForResult)
	defer cancel()

	// if waiting for a result times out, we still want to return the script's
	// execution request information along with the error, so that the caller can
	// use the execution id for later checks.
	timeoutResult := script
	checkInterval := time.Second
	after := time.NewTimer(checkInterval)
	for {
		select {
		case <-ctx.Done():
			return timeoutResult, ctx.Err()
		case <-after.C:
			result, err := svc.ds.GetHostScriptExecutionResult(ctx, script.ExecutionID)
			if err != nil {
				// is that due to the context being canceled during the DB access?
				if ctxErr := ctx.Err(); ctxErr != nil {
					return timeoutResult, ctxErr
				}
				return nil, ctxerr.Wrap(ctx, err, "get script execution result")
			}
			if result.ExitCode != nil {
				// a result was received from the host, return
				result.Hostname = host.DisplayName()
				return result, nil
			}

			// at a second to every attempt, until it reaches 5s (then check every 5s)
			if checkInterval < 5*time.Second {
				checkInterval += time.Second
			}
			after.Reset(checkInterval)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// Get script result for a host
////////////////////////////////////////////////////////////////////////////////

func getScriptResultEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.GetScriptResultRequest)
	scriptResult, err := svc.GetScriptResult(ctx, req.ExecutionID)
	if err != nil {
		return automdm.GetScriptResultResponse{Err: err}, nil
	}

	return setUpGetScriptResultResponse(scriptResult), nil
}

func setUpGetScriptResultResponse(scriptResult *automdm.HostScriptResult) *automdm.GetScriptResultResponse {
	hostTimeout := scriptResult.HostTimeout(scripts.MaxServerWaitTime)
	scriptResult.Message = scriptResult.UserMessage(hostTimeout, scriptResult.Timeout)

	return &automdm.GetScriptResultResponse{
		ScriptContents: scriptResult.ScriptContents,
		ScriptID:       scriptResult.ScriptID,
		ExitCode:       scriptResult.ExitCode,
		Output:         scriptResult.Output,
		Message:        scriptResult.Message,
		HostName:       scriptResult.Hostname,
		HostTimeout:    hostTimeout,
		HostID:         scriptResult.HostID,
		ExecutionID:    scriptResult.ExecutionID,
		Runtime:        scriptResult.Runtime,
		CreatedAt:      scriptResult.CreatedAt,
	}
}

func (svc *Service) GetScriptResult(ctx context.Context, execID string) (*automdm.HostScriptResult, error) {
	scriptResult, err := svc.ds.GetHostScriptExecutionResult(ctx, execID)
	if err != nil {
		if automdm.IsNotFound(err) {
			if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{}, automdm.ActionRead); err != nil {
				return nil, err
			}
		}
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "get script result")
	}

	if scriptResult.HostDeletedAt == nil {
		// host is not deleted, get it and authorize for the host's team
		host, err := svc.ds.HostLite(ctx, scriptResult.HostID)
		if err != nil {
			// if error is because the host does not exist, check first if the user
			// had access to run a script (to prevent leaking valid host ids).
			if automdm.IsNotFound(err) {
				if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{}, automdm.ActionRead); err != nil {
					return nil, err
				}
			}
			svc.authz.SkipAuthorization(ctx)
			return nil, ctxerr.Wrap(ctx, err, "get host lite")
		}
		if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{TeamID: host.TeamID}, automdm.ActionRead); err != nil {
			return nil, err
		}
		scriptResult.Hostname = host.DisplayName()
	} else {
		// host was deleted, authorize for no-team as a fallback
		if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{}, automdm.ActionRead); err != nil {
			return nil, err
		}
	}

	return scriptResult, nil
}

////////////////////////////////////////////////////////////////////////////////
// Create a (saved) script (via a multipart file upload)
////////////////////////////////////////////////////////////////////////////////

func createScriptEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.CreateScriptRequest)

	scriptFile, err := req.Script.Open()
	if err != nil {
		return &automdm.CreateScriptResponse{Err: err}, nil
	}
	defer scriptFile.Close()

	script, err := svc.NewScript(ctx, req.TeamID, filepath.Base(req.Script.Filename), scriptFile)
	if err != nil {
		return automdm.CreateScriptResponse{Err: err}, nil
	}
	return automdm.CreateScriptResponse{ScriptID: script.ID}, nil
}

func (svc *Service) NewScript(ctx context.Context, teamID *uint, name string, r io.Reader) (*automdm.Script, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "read script contents")
	}

	script := &automdm.Script{
		TeamID:         teamID,
		Name:           name,
		ScriptContents: file.Dos2UnixNewlines(string(b)),
	}

	if err := automdm.ValidateEmbeddedSecretsAndCustomHostVitals(ctx, svc.ds, []string{script.ScriptContents}); err != nil {
		return nil, automdm.NewInvalidArgumentError("script", err.Error())
	}

	if err := automdm.ValidateAutoMDMVariablesInScript(script.ScriptContents, license.IsPremium(ctx)); err != nil {
		return nil, err
	}

	if err := script.ValidateNewScript(); err != nil {
		return nil, automdm.NewInvalidArgumentError("script", err.Error())
	}

	savedScript, err := svc.ds.NewScript(ctx, script)
	if err != nil {
		var (
			existsErr automdm.AlreadyExistsError
			fkErr     automdm.ForeignKeyError
		)
		if errors.As(err, &existsErr) {
			err = automdm.NewInvalidArgumentError("script", "A script with this name already exists.").WithStatus(http.StatusConflict)
		} else if errors.As(err, &fkErr) {
			err = automdm.NewInvalidArgumentError("team_id", "The team does not exist.").WithStatus(http.StatusNotFound)
		}
		return nil, ctxerr.Wrap(ctx, err, "create script")
	}

	var teamName *string
	if teamID != nil && *teamID != 0 {
		tm, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, teamID, nil)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "get team name for create script activity")
		}
		teamName = &tm.Name
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeAddedScript{
			TeamID:     teamID,
			TeamName:   teamName,
			ScriptName: script.Name,
		},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "new activity for create script")
	}

	return savedScript, nil
}

////////////////////////////////////////////////////////////////////////////////
// Delete a (saved) script
////////////////////////////////////////////////////////////////////////////////

func deleteScriptEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.DeleteScriptRequest)
	err := svc.DeleteScript(ctx, req.ScriptID)
	if err != nil {
		return automdm.DeleteScriptResponse{Err: err}, nil
	}
	return automdm.DeleteScriptResponse{}, nil
}

func (svc *Service) DeleteScript(ctx context.Context, scriptID uint) error {
	script, err := svc.authorizeScriptByID(ctx, scriptID, automdm.ActionWrite)
	if err != nil {
		return err
	}

	if err := svc.ds.DeleteScript(ctx, script.ID); err != nil {
		return ctxerr.Wrap(ctx, err, "delete script")
	}

	var teamName *string
	if script.TeamID != nil && *script.TeamID != 0 {
		tm, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, script.TeamID, nil)
		if err != nil {
			return ctxerr.Wrap(ctx, err, "get team name for delete script activity")
		}
		teamName = &tm.Name
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeDeletedScript{
			TeamID:     script.TeamID,
			TeamName:   teamName,
			ScriptName: script.Name,
		},
	); err != nil {
		return ctxerr.Wrap(ctx, err, "new activity for delete script")
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// List (saved) scripts (paginated)
////////////////////////////////////////////////////////////////////////////////

func listScriptsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ListScriptsRequest)
	scripts, meta, err := svc.ListScripts(ctx, req.TeamID, req.ListOptions)
	if err != nil {
		return automdm.ListScriptsResponse{Err: err}, nil
	}
	return automdm.ListScriptsResponse{
		Meta:    meta,
		Scripts: scripts,
	}, nil
}

func (svc *Service) ListScripts(ctx context.Context, teamID *uint, opt automdm.ListOptions) ([]*automdm.Script, *automdm.PaginationMetadata, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: teamID}, automdm.ActionRead); err != nil {
		return nil, nil, err
	}

	// cursor-based pagination is not supported for scripts
	opt.After = ""
	// custom ordering is not supported, always by name
	opt.OrderKey = "name"
	opt.OrderDirection = automdm.OrderAscending
	// no matching query support
	opt.MatchQuery = ""
	// always include metadata for scripts
	opt.IncludeMetadata = true

	return svc.ds.ListScripts(ctx, teamID, opt)
}

////////////////////////////////////////////////////////////////////////////////
// Get/download a (saved) script
////////////////////////////////////////////////////////////////////////////////

func getScriptEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.GetScriptRequest)

	downloadRequested := req.Alt == "media"
	script, content, err := svc.GetScript(ctx, req.ScriptID, downloadRequested)
	if err != nil {
		return automdm.GetScriptResponse{Err: err}, nil
	}

	if downloadRequested {
		return automdm.DownloadFileResponse{
			Content:  content,
			Filename: fmt.Sprintf("%s %s", time.Now().Format(time.DateOnly), script.Name),
		}, nil
	}
	return automdm.GetScriptResponse{Script: script}, nil
}

func (svc *Service) GetScript(ctx context.Context, scriptID uint, withContent bool) (*automdm.Script, []byte, error) {
	script, err := svc.authorizeScriptByID(ctx, scriptID, automdm.ActionRead)
	if err != nil {
		return nil, nil, err
	}

	var content []byte
	if withContent {
		content, err = svc.ds.GetScriptContents(ctx, scriptID)
		if err != nil {
			return nil, nil, err
		}
	}
	return script, content, nil
}

////////////////////////////////////////////////////////////////////////////////
// Update Script Contents
////////////////////////////////////////////////////////////////////////////////

func updateScriptEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.UpdateScriptRequest)

	scriptFile, err := req.Script.Open()
	if err != nil {
		return &automdm.UpdateScriptResponse{Err: err}, nil
	}
	defer scriptFile.Close()

	script, err := svc.UpdateScript(ctx, req.ScriptID, scriptFile)
	if err != nil {
		return automdm.UpdateScriptResponse{Err: err}, nil
	}
	return automdm.UpdateScriptResponse{ScriptID: script.ID}, nil
}

func (svc *Service) UpdateScript(ctx context.Context, scriptID uint, r io.Reader) (*automdm.Script, error) {
	script, err := svc.ds.Script(ctx, scriptID)
	if err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "finding original script to update")
	}

	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: script.TeamID}, automdm.ActionWrite); err != nil {
		return nil, err
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "read script contents")
	}

	scriptContents := file.Dos2UnixNewlines(string(b))

	if err := automdm.ValidateEmbeddedSecretsAndCustomHostVitals(ctx, svc.ds, []string{scriptContents}); err != nil {
		return nil, automdm.NewInvalidArgumentError("script", err.Error())
	}

	if err := automdm.ValidateAutoMDMVariablesInScript(scriptContents, license.IsPremium(ctx)); err != nil {
		return nil, err
	}

	if err := (&automdm.Script{Name: script.Name, ScriptContents: scriptContents}).ValidateNewScript(); err != nil {
		return nil, automdm.NewInvalidArgumentError("script", err.Error())
	}

	// Update the script
	savedScript, err := svc.ds.UpdateScriptContents(ctx, scriptID, scriptContents)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "updating script contents")
	}

	var teamName *string
	if script.TeamID != nil && *script.TeamID != 0 {
		tm, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, script.TeamID, nil)
		if err != nil {
			return nil, ctxerr.Wrap(ctx, err, "get team name for create script activity")
		}
		teamName = &tm.Name
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeUpdatedScript{
			TeamID:     script.TeamID,
			TeamName:   teamName,
			ScriptName: script.Name,
		},
	); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "new activity for update script")
	}

	return savedScript, nil
}

////////////////////////////////////////////////////////////////////////////////
// Get Host Script Details
////////////////////////////////////////////////////////////////////////////////

func getHostScriptDetailsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.GetHostScriptDetailsRequest)
	var meta *automdm.PaginationMetadata
	scripts, _, err := hostByIDOrIdentifier(ctx, svc, req.Identifier, func(hostID uint) (scripts []*automdm.HostScriptDetail, err error) {
		scripts, meta, err = svc.GetHostScriptDetails(ctx, hostID, req.ListOptions)
		return scripts, err
	})
	if err != nil {
		return automdm.GetHostScriptDetailsResponse{Err: err}, nil
	}
	return automdm.GetHostScriptDetailsResponse{
		Scripts: scripts,
		Meta:    meta,
	}, nil
}

func (svc *Service) GetHostScriptDetails(ctx context.Context, hostID uint, opt automdm.ListOptions) ([]*automdm.HostScriptDetail, *automdm.PaginationMetadata, error) {
	h, err := svc.ds.HostLite(ctx, hostID)
	if err != nil {
		if automdm.IsNotFound(err) {
			// if error is because the host does not exist, check first if the user
			// had global access (to prevent leaking valid host ids).
			if err := svc.authz.Authorize(ctx, &automdm.Script{}, automdm.ActionRead); err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, err
	}

	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: h.TeamID}, automdm.ActionRead); err != nil {
		return nil, nil, err
	}

	// cursor-based pagination is not supported for scripts
	opt.After = ""
	// only name ordering is supported; direction comes from the request (ascending by default)
	opt.OrderKey = "name"
	// no matching query support
	opt.MatchQuery = ""
	// always include metadata for scripts
	opt.IncludeMetadata = true

	return svc.ds.GetHostScriptDetails(ctx, h.ID, h.TeamID, opt, h.Platform)
}

////////////////////////////////////////////////////////////////////////////////
// Batch Replace Scripts
////////////////////////////////////////////////////////////////////////////////

func batchSetScriptsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchSetScriptsRequest)
	scriptList, err := svc.BatchSetScripts(ctx, req.TeamID, req.TeamName, req.Scripts, req.DryRun)
	if err != nil {
		return automdm.BatchSetScriptsResponse{Err: err}, nil
	}
	return automdm.BatchSetScriptsResponse{Scripts: scriptList}, nil
}

func (svc *Service) BatchSetScripts(ctx context.Context, maybeTmID *uint, maybeTmName *string, payloads []automdm.ScriptPayload, dryRun bool) ([]automdm.ScriptResponse, error) {
	if maybeTmID != nil && maybeTmName != nil {
		svc.authz.SkipAuthorization(ctx) // so that the error message is not replaced by "forbidden"
		return nil, ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("team_name", "cannot specify both team_id and team_name"))
	}

	var teamID *uint
	var teamName *string

	if maybeTmID != nil || maybeTmName != nil {
		team, err := svc.EnterpriseOverrides.TeamByIDOrName(ctx, maybeTmID, maybeTmName)
		if err != nil {
			// On a dry run the team may not have been created yet. teamID then
			// stays nil (Unassigned), which is fine: a dry run returns before
			// anything automdm-specific is read or written.
			if !dryRun || !automdm.IsNotFound(err) {
				return nil, err
			}
		} else {
			teamID = &team.ID
			teamName = &team.Name
		}
	}

	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: teamID}, automdm.ActionWrite); err != nil {
		return nil, ctxerr.Wrap(ctx, err)
	}

	scripts := make([]*automdm.Script, 0, len(payloads))
	scriptNames := make([]string, 0, len(payloads))
	scriptContents := []string{}
	for i, p := range payloads {
		script := &automdm.Script{
			ScriptContents: string(p.ScriptContents),
			Name:           p.Name,
			TeamID:         teamID,
		}

		if err := script.ValidateNewScript(); err != nil {
			return nil, ctxerr.Wrap(ctx,
				automdm.NewInvalidArgumentError(fmt.Sprintf("scripts[%d]", i), err.Error()))
		}

		// unlike the embedded secrets validation below, this is a static check,
		// so it runs before the post-loop dryRun return to surface errors on
		// gitops dry runs
		if err := automdm.ValidateAutoMDMVariablesInScript(script.ScriptContents, license.IsPremium(ctx)); err != nil {
			// re-key validation errors on the indexed field, matching the rest
			// of this loop, so callers can tell which script failed
			var argErr *automdm.InvalidArgumentError
			if errors.As(err, &argErr) && len(argErr.Invalid()) > 0 {
				return nil, ctxerr.Wrap(ctx,
					automdm.NewInvalidArgumentError(fmt.Sprintf("scripts[%d]", i), argErr.Invalid()[0]["reason"]))
			}
			return nil, ctxerr.Wrap(ctx, err, "validate automdm variables in script")
		}

		scriptNames = append(scriptNames, script.Name)
		scriptContents = append(scriptContents, script.ScriptContents)
		scripts = append(scripts, script)
	}

	// any duplicate name in the provided set results in an error
	duplicateIndex, found, err := findDuplicateNameUnderCollation(ctx, svc.ds, scriptNames)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "checking for duplicate script names")
	}
	if found {
		return nil, ctxerr.Wrap(ctx,
			automdm.NewInvalidArgumentError(fmt.Sprintf("scripts[%d]", duplicateIndex),
				fmt.Sprintf("Couldn’t edit scripts. More than one script has the same file name: %q", scriptNames[duplicateIndex])),
			"duplicate script by name")
	}

	if dryRun {
		return nil, nil
	}

	if err := automdm.ValidateEmbeddedSecretsAndCustomHostVitals(ctx, svc.ds, scriptContents); err != nil {
		return nil, automdm.NewInvalidArgumentError("script", err.Error())
	}

	scriptResponses, err := svc.ds.BatchSetScripts(ctx, teamID, scripts)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "batch saving scripts")
	}

	if err := svc.NewActivity(
		ctx, authz.UserFromContext(ctx), &automdm.ActivityTypeEditedScript{
			TeamID:   teamID,
			TeamName: teamName,
		}); err != nil {
		return nil, ctxerr.Wrap(ctx, err, "logging activity for edited scripts")
	}
	return scriptResponses, nil
}

func batchScriptExecutionHostResultsEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchScriptExecutionHostResultsRequest)
	hosts, meta, count, err := svc.BatchScriptExecutionHostResults(ctx, req.BatchExecutionID, req.BatchExecutionStatus, req.ListOptions)
	if err != nil {
		return automdm.BatchScriptExecutionHostResultsResponse{Err: err}, nil
	}
	return automdm.BatchScriptExecutionHostResultsResponse{Hosts: hosts, Meta: *meta, Count: count}, nil
}

func batchScriptExecutionStatusEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchScriptExecutionStatusRequest)
	status, err := svc.BatchScriptExecutionStatus(ctx, req.BatchExecutionID)
	if err != nil {
		return automdm.BatchScriptExecutionStatusResponse{Err: err}, nil
	}
	return automdm.BatchScriptExecutionStatusResponse{BatchActivity: *status}, nil
}

func batchScriptExecutionListEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchScriptExecutionListRequest)

	page := 0
	pageSize := 0
	if req.Page != nil {
		page = int(*req.Page) //nolint:gosec // dismiss G115
	}
	if req.PerPage != nil {
		pageSize = int(*req.PerPage) //nolint:gosec // dismiss G115
	}
	// Set query offset based on the specified page and page size.
	offset := uint(page * pageSize) //nolint:gosec // dismiss G115
	filter := automdm.BatchExecutionStatusFilter{
		TeamID: &req.TeamID,
		Status: req.Status,
		Offset: &offset,
		Limit:  req.PerPage,
	}
	list, count, err := svc.BatchScriptExecutionList(ctx, filter)
	if err != nil {
		return automdm.BatchScriptExecutionStatusResponse{Err: err}, nil
	}
	// Get the # of results returned by this query.
	listSize := len(list)
	// We have previous results if we're not on the first page.
	hasPreviousResults := req.Page != nil && *req.Page > 0
	// Calculate the number of results on this page + all previous pages.
	resultsSeen := (page * pageSize) + listSize
	// If it's less than the total count, we have more results.
	hasNextResults := resultsSeen < int(count)
	return automdm.BatchScriptExecutionListResponse{
		BatchScriptExecutions: list,
		Count:                 uint(count), //nolint:gosec // dismiss G115
		Meta: automdm.PaginationMetadata{
			HasNextResults:     hasNextResults,
			HasPreviousResults: hasPreviousResults,
		},
	}, nil
}

func batchScriptCancelEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchScriptCancelRequest)
	if err := svc.BatchScriptCancel(ctx, req.BatchExecutionID); err != nil {
		return automdm.BatchScriptCancelResponse{Err: err}, nil
	}

	return automdm.BatchScriptCancelResponse{}, nil
}

func (svc *Service) BatchScriptCancel(ctx context.Context, batchExecutionID string) error {
	summaryList, err := svc.ds.ListBatchScriptExecutions(ctx, automdm.BatchExecutionStatusFilter{
		ExecutionID: &batchExecutionID,
	})
	if err != nil && !automdm.IsNotFound(err) {
		svc.authz.SkipAuthorization(ctx)
		return ctxerr.Wrap(ctx, err, "get batch script summary")
	} else if err != nil && automdm.IsNotFound(err) {
		if authErr := svc.authz.Authorize(ctx, &automdm.Script{}, automdm.ActionRead); authErr != nil {
			return authErr
		}
		return err // return the not found from the db.
	}

	if len(summaryList) > 1 {
		return ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("batch_execution_id", "expected a single batch execution status, got multiple"))
	}

	summary := (summaryList)[0]

	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: summary.TeamID}, automdm.ActionWrite); err != nil {
		return err
	}
	if err := svc.ds.CancelBatchScript(ctx, batchExecutionID); err != nil {
		return ctxerr.Wrap(ctx, err, "canceling batch script")
	}

	// Read from the primary, a replica may not have the canceled count written above yet.
	batchActivity, err := svc.ds.GetBatchActivity(ctxdb.RequirePrimary(ctx, true), batchExecutionID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "getting canceled activity stats")
	}

	ctxUser := authz.UserFromContext(ctx)

	targeted := uint(0)
	if batchActivity.NumTargeted != nil {
		// No nil dereference in case this is not set for some reason
		targeted = *batchActivity.NumTargeted
	}

	canceled := uint(0)
	if batchActivity.NumCanceled != nil {
		canceled = *batchActivity.NumCanceled
	}

	if err := svc.NewActivity(ctx, ctxUser, automdm.ActivityTypeBatchScriptCanceled{
		BatchExecutionID: batchExecutionID,
		ScriptName:       batchActivity.ScriptName,
		HostCount:        targeted,
		CanceledCount:    canceled,
	}); err != nil {
		return ctxerr.Wrap(ctx, err, "creating activity for cancel batch script")
	}

	return nil
}

func (svc *Service) BatchScriptExecutionStatus(ctx context.Context, batchExecutionID string) (*automdm.BatchActivity, error) {
	summaryList, err := svc.ds.ListBatchScriptExecutions(ctx, automdm.BatchExecutionStatusFilter{
		ExecutionID: &batchExecutionID,
	})
	if err != nil && !automdm.IsNotFound(err) {
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "get batch script summary")
	} else if err != nil && automdm.IsNotFound(err) {
		if authErr := svc.authz.Authorize(ctx, &automdm.Script{}, automdm.ActionRead); authErr != nil {
			return nil, authErr
		}
		return nil, err // return the not found from the db.
	}

	if len(summaryList) > 1 {
		return nil, ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("batch_execution_id", "expected a single batch execution status, got multiple"))
	}

	summary := (summaryList)[0]

	if authErr := svc.authz.Authorize(ctx, &automdm.Script{TeamID: summary.TeamID}, automdm.ActionRead); authErr != nil {
		return nil, authErr
	}

	return &summary, nil
}

func (svc *Service) BatchScriptExecutionHostResults(ctx context.Context, batchExecutionID string, status automdm.BatchScriptExecutionStatus, opt automdm.ListOptions) (hosts []automdm.BatchScriptHost, meta *automdm.PaginationMetadata, count uint, err error) {
	// Get the batch activity.
	batchActivity, err := svc.ds.GetBatchActivity(ctx, batchExecutionID)
	if err != nil {
		if !automdm.IsNotFound(err) {
			svc.authz.SkipAuthorization(ctx)
		} else if authErr := svc.authz.Authorize(ctx, &automdm.Script{}, automdm.ActionRead); authErr != nil {
			return nil, nil, 0, authErr
		}
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "getting batch activity")
	}
	if batchActivity.ScriptID == nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "batch activity has no script ID")
	}

	// Get the script referred to by the batch activity.
	script, err := svc.ds.Script(ctx, *batchActivity.ScriptID)
	if err != nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "getting script")
	}
	if script == nil {
		svc.authz.SkipAuthorization(ctx)
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "script not found")
	}

	// Authorize based on the script's team ID.
	if err = svc.authz.Authorize(ctx, &automdm.Script{TeamID: script.TeamID}, automdm.ActionRead); err != nil {
		return nil, nil, 0, err
	}

	// Validate the supplied batch execution status.
	if !status.IsValid() {
		return nil, nil, 0, automdm.NewInvalidArgumentError("batch_execution_status", "invalid batch execution status")
	}

	// Always include pagination info.
	opt.IncludeMetadata = true
	// Default sort order is name ascending.
	if opt.OrderKey == "" {
		opt.OrderKey = "display_name"
		opt.OrderDirection = automdm.OrderAscending
	}

	hosts, meta, count, err = svc.ds.ListBatchScriptHosts(ctx, batchExecutionID, status, opt)
	if err != nil {
		return nil, nil, 0, ctxerr.Wrap(ctx, err, "list batch script hosts")
	}

	return hosts, meta, count, nil
}

func (svc *Service) BatchScriptExecutionList(ctx context.Context, filter automdm.BatchExecutionStatusFilter) ([]automdm.BatchActivity, int64, error) {
	if err := svc.authz.Authorize(ctx, &automdm.Script{TeamID: filter.TeamID}, automdm.ActionRead); err != nil {
		return nil, 0, err
	}
	// Get the count first.
	count, err := svc.ds.CountBatchScriptExecutions(ctx, filter)
	if err != nil {
		return nil, 0, nil
	}

	summaryList, err := svc.ds.ListBatchScriptExecutions(ctx, filter)
	if err != nil {
		return nil, 0, ctxerr.Wrap(ctx, err, "get batch script list")
	}

	return summaryList, count, nil
}

func (svc *Service) authorizeScriptByID(ctx context.Context, scriptID uint, authzAction string) (*automdm.Script, error) {
	// first, get the script because we don't know which team id it is for.
	script, err := svc.ds.Script(ctx, scriptID)
	if err != nil {
		if automdm.IsNotFound(err) {
			// couldn't get the script to have its team, authorize with a no-team
			// script as a fallback - the requested script does not exist so there's
			// no way to know what team it would be for, and returning a 404 without
			// authorization would leak the existing/non existing ids.
			if err := svc.authz.Authorize(ctx, &automdm.Script{}, authzAction); err != nil {
				return nil, err
			}
		}
		svc.authz.SkipAuthorization(ctx)
		return nil, ctxerr.Wrap(ctx, err, "get script")
	}

	// do the actual authorization with the script's team id
	if err := svc.authz.Authorize(ctx, script, authzAction); err != nil {
		return nil, err
	}
	return script, nil
}

////////////////////////////////////////////////////////////////////////////////
// Bulk script execution
////////////////////////////////////////////////////////////////////////////////

func batchScriptRunEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.BatchScriptRunRequest)
	batchID, err := svc.BatchScriptExecute(ctx, req.ScriptID, req.HostIDs, req.Filters, req.NotBefore)
	if err != nil {
		return automdm.BatchScriptRunResponse{Err: err}, nil
	}
	return automdm.BatchScriptRunResponse{BatchExecutionID: batchID}, nil
}

const MAX_BATCH_EXECUTION_HOSTS = 5000

func (svc *Service) BatchScriptExecute(ctx context.Context, scriptID uint, hostIDs []uint, filters *map[string]any, notBefore *time.Time) (string, error) {
	// If we are given both host IDs and filters, return an error
	if len(hostIDs) > 0 && filters != nil {
		return "", automdm.NewInvalidArgumentError("filters", "cannot specify both host_ids and filters")
	}

	// Use the authorize script by ID to handle authz
	script, err := svc.authorizeScriptByID(ctx, scriptID, automdm.ActionWrite)
	if err != nil {
		return "", err
	}

	// Authorize the actual execution with the script's team
	if err := svc.authz.Authorize(ctx, &automdm.HostScriptResult{TeamID: script.TeamID}, automdm.ActionWrite); err != nil {
		return "", err
	}

	var userId *uint
	ctxUser := authz.UserFromContext(ctx)
	if ctxUser != nil {
		userId = &ctxUser.ID
	}

	var hosts []*automdm.Host

	// If we are given filters, we need to get the hosts matching those filters
	if filters != nil {
		opt, lid, err := hostListOptionsFromFilters(filters)
		if err != nil {
			return "", err
		}

		if opt == nil {
			return "", automdm.NewInvalidArgumentError("filters", "filters must be a valid set of host list options")
		}

		if opt.TeamFilter == nil {
			return "", automdm.NewInvalidArgumentError("filters", "filters must include a team filter")
		}

		filter := automdm.TeamFilter{User: ctxUser, IncludeObserver: true}

		// Load hosts, either from label if provided or from all hosts.
		if lid != nil {
			hosts, err = svc.ds.ListHostsInLabel(ctx, filter, *lid, *opt)
		} else {
			opt.DisableIssues = true // intentionally ignore failing policies
			hosts, err = svc.ds.ListHosts(ctx, filter, *opt)
		}

		if err != nil {
			return "", err
		}
	} else {
		// Get the hosts matching the host IDs
		hosts, err = svc.ds.ListHostsLiteByIDs(ctx, hostIDs)
		if err != nil {
			return "", err
		}
	}
	if len(hosts) == 0 {
		return "", &automdm.BadRequestError{Message: "no hosts match the specified host IDs"}
	}

	if len(hosts) > MAX_BATCH_EXECUTION_HOSTS {
		return "", automdm.NewInvalidArgumentError("filters", "too_many_hosts")
	}

	hostIDsToExecute := make([]uint, 0, len(hosts))
	for _, host := range hosts {
		hostIDsToExecute = append(hostIDsToExecute, host.ID)
		if host.TeamID == nil && script.TeamID == nil {
			continue
		}
		if host.TeamID == nil || script.TeamID == nil || *host.TeamID != *script.TeamID {
			return "", automdm.NewInvalidArgumentError("host_ids", "all hosts must be on the same team as the script")
		}
	}

	if notBefore == nil || notBefore.Before(time.Now()) {
		batchID, err := svc.ds.NewBatchScriptExecution(ctx, userId, scriptID, hostIDsToExecute)
		if err != nil {
			return "", automdm.NewUserMessageError(err, http.StatusBadRequest)
		}

		// Insert script executions and activities in a separate goroutine, because it can be quite
		// slow for a large number of hosts, and we just need to return the batchID here.
		// Read the batch from the primary, a replica can lag behind the batch row written above.
		go svc.runBatchScriptExecution(ctxdb.RequirePrimary(context.WithoutCancel(ctx), true), batchID)

		if err := svc.NewActivity(ctx, ctxUser, automdm.ActivityTypeRanScriptBatch{
			ScriptName:       script.Name,
			BatchExecutionID: batchID,
			HostCount:        uint(len(hostIDsToExecute)),
			TeamID:           script.TeamID,
		}); err != nil {
			return "", ctxerr.Wrap(ctx, err, "creating activity for batch run scripts")
		}

		return batchID, nil
	}

	notBeforeUTC := notBefore.UTC()
	batchID, err := svc.ds.BatchScheduleScript(ctx, userId, scriptID, hostIDsToExecute, notBeforeUTC)
	if err != nil {
		return "", automdm.NewUserMessageError(err, http.StatusBadRequest)
	}

	if err := svc.NewActivity(ctx, ctxUser, automdm.ActivityTypeBatchScriptScheduled{
		ScriptName:       &script.Name,
		BatchExecutionID: batchID,
		HostCount:        uint(len(hostIDsToExecute)),
		TeamID:           script.TeamID,
		NotBefore:        &notBeforeUTC,
	}); err != nil {
		return "", ctxerr.Wrap(ctx, err, "creating activity for scheduled batch run scripts")
	}

	return batchID, nil
}

func (svc *Service) runBatchScriptExecution(ctx context.Context, batchID string) {
	err := svc.ds.RunScheduledBatchActivity(ctx, batchID)
	if err != nil {
		svc.logger.ErrorContext(ctx, "run batch script execution", "batch_execution_id", batchID, "err", err)
		ctxerr.Handle(ctx, err)
	}
}

////////////////////////////////////////////////////////////////////////////////
// Lock host
////////////////////////////////////////////////////////////////////////////////

func lockHostEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.LockHostRequest)
	unlockPIN, err := svc.LockHost(ctx, req.HostID, req.ViewPin)
	if err != nil {
		return automdm.LockHostResponse{Err: err}, nil
	}
	// We bail from locking if the host is locked or wiped, so we can assume the host is unlocked at this point
	response := &automdm.LockHostResponse{DeviceStatus: automdm.DeviceStatusUnlocked, PendingAction: automdm.PendingActionLock}

	if req.ViewPin && unlockPIN != "" {
		response.UnlockPIN = unlockPIN
	}
	return response, nil
}

func (svc *Service) LockHost(ctx context.Context, _ uint, _ bool) (string, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return "", automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Unlock host
////////////////////////////////////////////////////////////////////////////////

func unlockHostEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.UnlockHostRequest)
	res, err := svc.UnlockHost(ctx, req.HostID)
	if err != nil {
		return automdm.UnlockHostResponse{Err: err}, nil
	}

	// Only macOS and Android return an unlock PIN; for other platforms the UnlockHost call
	// triggers the unlocking without further user action.
	return automdm.UnlockHostResponse{
		HostID:        &req.HostID,
		UnlockPIN:     res.UnlockPIN,
		LockedAt:      res.LockedAt,
		DeviceStatus:  res.DeviceStatus,
		PendingAction: res.PendingAction,
	}, nil
}

func (svc *Service) UnlockHost(ctx context.Context, hostID uint) (*automdm.HostUnlockResult, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}

////////////////////////////////////////////////////////////////////////////////
// Wipe host
////////////////////////////////////////////////////////////////////////////////

func wipeHostEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.WipeHostRequest)
	if err := svc.WipeHost(ctx, req.HostID); err != nil {
		return automdm.WipeHostResponse{Err: err}, nil
	}
	// WipeHost refuses a locked or wiped host. A wipe queued behind an acknowledged reset is reported as unlocked here too,
	// although getting the host shows its reset status.
	return automdm.WipeHostResponse{DeviceStatus: automdm.DeviceStatusUnlocked, PendingAction: automdm.PendingActionWipe}, nil
}

func (svc *Service) WipeHost(ctx context.Context, hostID uint) error {
	// First ensure the user has access to list hosts, then check the specific
	// host once team_id is loaded.
	if err := svc.authz.Authorize(ctx, &automdm.Host{}, automdm.ActionList); err != nil {
		return authz.GitOpsHostListError(ctx, err)
	}
	host, err := svc.ds.Host(ctx, hostID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get host")
	}

	// Authorize again with team loaded now that we have the host's team_id.
	// Authorize as "execute mdm_command", which is the correct access
	// requirement and is what happens for macOS platforms.
	notFoundErr := ctxerr.Wrap(ctx, common_mysql.NotFound("Host").WithID(hostID), "wipe host")
	if err := svc.authz.AuthorizeOrNotFound(ctx, automdm.MDMCommandAuthz{TeamID: host.TeamID}, automdm.ActionWrite, notFoundErr); err != nil {
		return authz.GitOpsMDMCommandError(ctx, host.TeamID, err)
	}

	// On AutoMDM Free, Wipe is only available for Android hosts. Wipe for macOS, Windows, Linux and iOS/iPadOS
	// remains an AutoMDM Premium feature, implemented by the ee WipeHost method which shadows this one on Premium
	// deployments (so Android-on-Premium also goes through ee, not here).
	if host.AutoMDMPlatform() != "android" {
		return automdm.ErrMissingLicense
	}

	if err := automdm.ValidateAndroidWipeRequest(ctx, svc.ds, host); err != nil {
		return ctxerr.Wrap(ctx, err, "validate android wipe request")
	}

	// the wipe command requires the host to be MDM-enrolled in AutoMDM
	connected, err := svc.ds.IsHostConnectedToAutoMDMMDM(ctx, host)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "checking if host is connected to AutoMDM")
	}
	if !connected {
		return ctxerr.Wrap(ctx, automdm.NewInvalidArgumentError("host_id", "Can't wipe the host because it doesn't have MDM turned on."))
	}

	// validations based on host's actions status (pending lock, unlock, wipe)
	lockWipe, err := svc.ds.GetHostLockWipeStatus(ctx, host)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get host lock/wipe status")
	}
	if err := lockWipe.ValidateWipeRequest(); err != nil {
		return ctxerr.Wrap(ctx, err)
	}

	// all good, go ahead with queuing the wipe request.
	if err := svc.androidSvc.WipeAndroidHost(ctx, host.ID); err != nil {
		return ctxerr.Wrap(ctx, err, "enqueuing wipe request for android")
	}

	if err := svc.NewActivity(
		ctx,
		authz.UserFromContext(ctx),
		automdm.ActivityTypeWipedHost{
			HostID:          host.ID,
			HostDisplayName: host.DisplayName(),
			HostPlatform:    host.AutoMDMPlatform(),
		},
	); err != nil {
		return ctxerr.Wrap(ctx, err, "create activity for wipe host request")
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// Reset host
////////////////////////////////////////////////////////////////////////////////

func resetHostEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*automdm.ResetHostRequest)
	if err := svc.ResetHost(ctx, req.HostID); err != nil {
		return automdm.ResetHostResponse{Err: err}, nil
	}
	// A locked, wiped or reset host is rejected, so the host is unlocked here.
	return automdm.ResetHostResponse{DeviceStatus: automdm.DeviceStatusUnlocked, PendingAction: automdm.PendingActionReset}, nil
}

func (svc *Service) ResetHost(ctx context.Context, hostID uint) error {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return automdm.ErrMissingLicense
}
