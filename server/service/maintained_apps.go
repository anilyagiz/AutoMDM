package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	maintained_apps "github.com/anilyagiz/automdm/v4/server/mdm/maintainedapps"
	"github.com/anilyagiz/automdm/v4/server/platform/endpointer"
)

type addAutoMDMMaintainedAppRequest struct {
	TeamID            *uint    `json:"team_id"`
	AppID             uint     `json:"automdm_maintained_app_id"`
	InstallScript     string   `json:"install_script"`
	PreInstallQuery   string   `json:"pre_install_query"` //nolint:apiparamcheck
	PostInstallScript string   `json:"post_install_script"`
	SelfService       bool     `json:"self_service"`
	UninstallScript   string   `json:"uninstall_script"`
	LabelsIncludeAny  []string `json:"labels_include_any"`
	LabelsExcludeAny  []string `json:"labels_exclude_any"`
	LabelsIncludeAll  []string `json:"labels_include_all"`
	AutomaticInstall  bool     `json:"automatic_install"`
	Categories        []string `json:"categories"`
}

// DecodeRequest implements the RequestDecoder interface to support base64-encoded
// script fields. This allows bypassing WAF rules that may block requests containing
// shell/PowerShell script patterns. When the X-Automdm-Scripts-Encoded header is set
// to "base64", the script fields are decoded from base64.
func (addAutoMDMMaintainedAppRequest) DecodeRequest(ctx context.Context, r *http.Request) (any, error) {
	var req addAutoMDMMaintainedAppRequest

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, &automdm.BadRequestError{Message: "failed to read request body", InternalErr: err}
	}
	if !endpointer.SkipsRemovedNamesScan(r.URL.Path) {
		if err := endpointer.RejectRemovedJSONKeys(body, reflect.TypeFor[addAutoMDMMaintainedAppRequest]()); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, &automdm.BadRequestError{
			Message:     "failed to decode request body",
			InternalErr: err,
		}
	}

	// Check if scripts are base64 encoded
	if isScriptsEncoded(r) {
		var err error
		if req.InstallScript, err = decodeBase64Script(req.InstallScript); err != nil {
			return nil, automdm.NewInvalidArgumentError("install_script", "invalid base64 encoding")
		}
		if req.UninstallScript, err = decodeBase64Script(req.UninstallScript); err != nil {
			return nil, automdm.NewInvalidArgumentError("uninstall_script", "invalid base64 encoding")
		}
		if req.PostInstallScript, err = decodeBase64Script(req.PostInstallScript); err != nil {
			return nil, automdm.NewInvalidArgumentError("post_install_script", "invalid base64 encoding")
		}
		if req.PreInstallQuery, err = decodeBase64Script(req.PreInstallQuery); err != nil {
			return nil, automdm.NewInvalidArgumentError("pre_install_query", "invalid base64 encoding")
		}
	}

	return &req, nil
}

type addAutoMDMMaintainedAppResponse struct {
	SoftwareTitleID uint  `json:"software_title_id,omitempty"`
	Err             error `json:"error,omitempty"`
}

func (r addAutoMDMMaintainedAppResponse) Error() error { return r.Err }

func addAutoMDMMaintainedAppEndpoint(ctx context.Context, request interface{}, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*addAutoMDMMaintainedAppRequest)
	ctx, cancel := context.WithTimeout(ctx, maintained_apps.InstallerTimeout)
	defer cancel()
	titleId, err := svc.AddAutoMDMMaintainedApp(
		ctx,
		req.TeamID,
		req.AppID,
		req.InstallScript,
		req.PreInstallQuery,
		req.PostInstallScript,
		req.UninstallScript,
		req.SelfService,
		req.AutomaticInstall,
		req.LabelsIncludeAny,
		req.LabelsExcludeAny,
		req.LabelsIncludeAll,
	)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			err = automdm.NewGatewayTimeoutError(automdm.AddMaintainedAppTimeoutErrMsg, err)
		case errors.Is(err, context.Canceled):
			err = automdm.NewGatewayTimeoutError(automdm.AddMaintainedAppCanceledErrMsg, err)
		}
		return &addAutoMDMMaintainedAppResponse{Err: err}, nil
	}
	return &addAutoMDMMaintainedAppResponse{SoftwareTitleID: titleId}, nil
}

func (svc *Service) AddAutoMDMMaintainedApp(ctx context.Context, _ *uint, _ uint, _, _, _, _ string, _ bool, _ bool, _, _, _ []string) (uint, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return 0, automdm.ErrMissingLicense
}

type listAutoMDMMaintainedAppsRequest struct {
	automdm.ListOptions
	TeamID *uint `query:"team_id,optional"`
	// Platform optionally filters to apps available on the given platform
	// ("darwin" or "windows").
	Platform string `query:"platform,optional"`
	// AvailableOnly, when true, returns only apps not yet added to the team
	// (the "Hide added apps" filter).
	AvailableOnly bool `query:"available,optional"`
}

type listAutoMDMMaintainedAppsResponse struct {
	AutoMDMMaintainedApps []automdm.MaintainedApp     `json:"automdm_maintained_apps"`
	Count                 int                         `json:"count"`
	Meta                  *automdm.PaginationMetadata `json:"meta"`
	Err                   error                       `json:"error,omitempty"`
}

func (r listAutoMDMMaintainedAppsResponse) Error() error { return r.Err }

func listAutoMDMMaintainedAppsEndpoint(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*listAutoMDMMaintainedAppsRequest)

	opts := automdm.MaintainedAppListOptions{
		ListOptions:   req.ListOptions,
		Platform:      req.Platform,
		AvailableOnly: req.AvailableOnly,
	}

	apps, meta, err := svc.ListAutoMDMMaintainedApps(ctx, req.TeamID, opts)
	if err != nil {
		return listAutoMDMMaintainedAppsResponse{Err: err}, nil
	}

	listResp := listAutoMDMMaintainedAppsResponse{
		AutoMDMMaintainedApps: apps,
		Meta:                  meta,
	}
	if meta != nil {
		listResp.Count = int(meta.TotalResults) //nolint:gosec // dismiss G115
	}

	return listResp, nil
}

func (svc *Service) ListAutoMDMMaintainedApps(ctx context.Context, teamID *uint, opts automdm.MaintainedAppListOptions) ([]automdm.MaintainedApp, *automdm.PaginationMetadata, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, nil, automdm.ErrMissingLicense
}

type getAutoMDMMaintainedAppRequest struct {
	AppID  uint  `url:"app_id"`
	TeamID *uint `query:"team_id,optional"`
}

type getAutoMDMMaintainedAppResponse struct {
	AutoMDMMaintainedApp *automdm.MaintainedApp `json:"automdm_maintained_app"`
	Err                  error                  `json:"error,omitempty"`
}

func (r getAutoMDMMaintainedAppResponse) Error() error { return r.Err }

func getAutoMDMMaintainedApp(ctx context.Context, request any, svc automdm.Service) (automdm.Errorer, error) {
	req := request.(*getAutoMDMMaintainedAppRequest)

	app, err := svc.GetAutoMDMMaintainedApp(ctx, req.AppID, req.TeamID)
	if err != nil {
		return getAutoMDMMaintainedAppResponse{Err: err}, nil
	}

	return getAutoMDMMaintainedAppResponse{AutoMDMMaintainedApp: app}, nil
}

func (svc *Service) GetAutoMDMMaintainedApp(ctx context.Context, appID uint, teamID *uint) (*automdm.MaintainedApp, error) {
	// skipauth: No authorization check needed due to implementation returning
	// only license error.
	svc.authz.SkipAuthorization(ctx)

	return nil, automdm.ErrMissingLicense
}
