package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/anilyagiz/automdm/v4/pkg/spec"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/go-kit/kit/endpoint"
)

type setupRequest struct {
	Admin        *automdm.UserPayload `json:"admin"`
	OrgInfo      *automdm.OrgInfo     `json:"org_info"`
	ServerURL    *string              `json:"server_url,omitempty"`
	EnrollSecret *string              `json:"osquery_enroll_secret,omitempty"`
}

type setupResponse struct {
	Admin        *automdm.User    `json:"admin,omitempty"`
	OrgInfo      *automdm.OrgInfo `json:"org_info,omitempty"`
	ServerURL    *string          `json:"server_url"`
	EnrollSecret *string          `json:"osquery_enroll_secret"`
	Token        *string          `json:"token,omitempty"`
	Err          error            `json:"error,omitempty"`
}

func (r setupResponse) Error() error { return r.Err }

func makeSetupEndpoint(svc automdm.Service, logger *slog.Logger, applyStarterLibrary func(ctx context.Context, serverURL, token string) error) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(setupRequest)
		if req.Admin == nil {
			return setupResponse{Err: ctxerr.New(ctx, "setup request must provide admin")}, nil
		}

		adminPayload := *req.Admin
		if adminPayload.Email == nil || *adminPayload.Email == "" {
			err := ctxerr.New(ctx, "admin email cannot be empty")
			return setupResponse{Err: err}, nil
		}
		if adminPayload.Password == nil || *adminPayload.Password == "" {
			err := ctxerr.New(ctx, "admin password cannot be empty")
			return setupResponse{Err: err}, nil
		}
		// The admin is created first and closes setup, so an invalid server URL must be rejected here.
		if err := validateSetupServerURL(ctx, ptr.ValOrZero(req.ServerURL)); err != nil {
			return setupResponse{Err: err}, nil
		}

		// Creating the initial admin is the only race-safe guard on this
		// unauthenticated endpoint, so it must succeed before app config and
		// enroll secrets are overwritten.
		adminPayload.GlobalRole = ptr.String(automdm.RoleAdmin)
		admin, err := svc.CreateInitialUser(ctx, adminPayload)
		if err != nil {
			return setupResponse{Err: err}, nil
		}

		config := &automdm.AppConfig{}
		if req.OrgInfo != nil {
			config.OrgInfo = *req.OrgInfo
		}
		if req.ServerURL != nil {
			config.ServerSettings.ServerURL = *req.ServerURL
		}
		config, err = svc.NewAppConfig(ctx, *config)
		if err != nil {
			logger.ErrorContext(ctx, "setup created the initial admin but failed to save app config and enroll secret", "err", err)
			return setupResponse{Err: err}, nil
		}

		// If everything works to this point, log the user in and return token.
		// If the login fails for some reason, ignore the error and don't return
		// a token, forcing the user to log in manually.
		var token *string
		_, session, err := svc.Login(ctx, *req.Admin.Email, *req.Admin.Password, false)
		if err != nil {
			logger.DebugContext(ctx, "setup login", "endpoint", "setup", "op", "login", "err", err)
		} else {
			token = &session.Key

			// Apply starter library using the admin token we just created
			if req.ServerURL != nil {
				if err := applyStarterLibrary(ctx, *req.ServerURL, session.Key); err != nil {
					logger.DebugContext(ctx, "setup apply starter library", "endpoint", "setup", "op", "applyStarterLibrary", "err", err)
					// Continue even if there's an error applying the starter library
				}
			} else {
				logger.DebugContext(ctx, "Skipping starter library application due to missing server URL", "endpoint", "setup")
			}
		}

		return setupResponse{
			Admin:     admin,
			OrgInfo:   &config.OrgInfo,
			ServerURL: req.ServerURL,
			Token:     token,
		}, nil
	}
}

// ApplyStarterLibrary scaffolds the starter GitOps templates via `automdmctl new`
// and applies them via `automdmctl gitops`, producing the same result as a user
// running those commands manually.
//
// The runAutoMDMctl callback should run the automdmctl CLI with the given arguments.
// This keeps the CLI dependency out of the service package.
func ApplyStarterLibrary(
	ctx context.Context,
	serverURL string,
	token string,
	logger *slog.Logger,
	runAutoMDMctl func(args []string) error,
) error {
	logger.DebugContext(ctx, "Applying starter library")

	// Create an authenticated client to fetch app config.
	client, err := NewClient(serverURL, true, "", "")
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	client.SetToken(token)

	appConfig, err := client.GetAppConfig()
	if err != nil {
		return fmt.Errorf("failed to get app config: %w", err)
	}

	orgName := appConfig.OrgInfo.OrgName
	if orgName == "" {
		orgName = "AutoMDM"
	}

	// Create a temp directory for the rendered templates.
	tempDir, err := os.MkdirTemp("", "automdm-starter-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	outDir := filepath.Join(tempDir, "gitops")

	// Render templates using `automdmctl new`.
	if err := runAutoMDMctl([]string{"new", "--org-name", orgName, "--dir", outDir}); err != nil {
		return fmt.Errorf("automdmctl new: %w", err)
	}

	// Set env overrides so GitOpsFromFile can expand $AUTOMDM_URL without
	// polluting the process environment.
	spec.SetEnvOverrides(map[string]string{
		"AUTOMDM_URL": serverURL,
	})
	defer spec.SetEnvOverrides(nil)

	// Write a temporary automdmctl config file with auth credentials.
	configFile, err := os.CreateTemp(tempDir, "automdmctl-config-*.yml")
	if err != nil {
		return fmt.Errorf("failed to create automdmctl config: %w", err)
	}
	fmt.Fprintf(configFile, "contexts:\n  default:\n    address: %s\n    tls-skip-verify: true\n    token: %s\n",
		serverURL, token)
	configFile.Close()

	// Build the gitops args: global config first, then team configs (premium only).
	args := []string{"gitops", "--config", configFile.Name(), "-f", filepath.Join(outDir, "default.yml")}

	if appConfig.License != nil && appConfig.License.IsPremium() {
		automdmDir := filepath.Join(outDir, "teams")
		entries, err := os.ReadDir(automdmDir)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to read teams directory: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
				continue
			}
			args = append(args, "-f", filepath.Join(automdmDir, entry.Name()))
		}
	}

	if err := runAutoMDMctl(args); err != nil {
		return fmt.Errorf("automdmctl gitops: %w", err)
	}

	logger.DebugContext(ctx, "Starter library applied successfully")
	return nil
}
