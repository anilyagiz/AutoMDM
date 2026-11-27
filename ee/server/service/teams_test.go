package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	mdmtest "github.com/anilyagiz/automdm/v4/server/mdm/testing_utils"
	"github.com/anilyagiz/automdm/v4/server/mock"
	svcmock "github.com/anilyagiz/automdm/v4/server/mock/service"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestNewTeamNameValidation(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		team.ID = 1
		return team, nil
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}

	adminUser := &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	ctx := test.UserContext(context.Background(), adminUser)

	testCases := []struct {
		name     string
		teamName *string
		wantErr  string
		wantName string
	}{
		{
			name:    "nil name",
			wantErr: "missing required argument",
		},
		{
			name:     "empty string",
			teamName: ptr.String(""),
			wantErr:  "may not be empty",
		},
		{
			name:     "only spaces",
			teamName: ptr.String("     "),
			wantErr:  "may not be empty",
		},
		{
			name:     "only tabs",
			teamName: ptr.String("\t\t\t"),
			wantErr:  "may not be empty",
		},
		{
			name:     "only newlines",
			teamName: ptr.String("\n\n\n"),
			wantErr:  "may not be empty",
		},
		{
			name:     "only carriage returns",
			teamName: ptr.String("\r\r\r"),
			wantErr:  "may not be empty",
		},
		{
			name:     "mixed whitespace",
			teamName: ptr.String(" \t\n\r "),
			wantErr:  "may not be empty",
		},
		{
			name:     "single space",
			teamName: ptr.String(" "),
			wantErr:  "may not be empty",
		},
		{
			name:     "single tab",
			teamName: ptr.String("\t"),
			wantErr:  "may not be empty",
		},
		{
			name:     "single newline",
			teamName: ptr.String("\n"),
			wantErr:  "may not be empty",
		},
		{
			name:     "leading spaces are trimmed",
			teamName: ptr.String("   myteam"),
			wantName: "myteam",
		},
		{
			name:     "trailing spaces are trimmed",
			teamName: ptr.String("myteam   "),
			wantName: "myteam",
		},
		{
			name:     "inner spaces preserved",
			teamName: ptr.String("my team"),
			wantName: "my team",
		},
		{
			name:     "leading and trailing trimmed with inner preserved",
			teamName: ptr.String("  my team  "),
			wantName: "my team",
		},
		{
			name:     "valid name no trimming needed",
			teamName: ptr.String("Engineering"),
			wantName: "Engineering",
		},
		{
			name:     "name at max length is accepted",
			teamName: new(strings.Repeat("a", automdm.MaxTeamNameLength)),
			wantName: strings.Repeat("a", automdm.MaxTeamNameLength),
		},
		{
			name:     "name over max length is rejected",
			teamName: new(strings.Repeat("a", automdm.MaxTeamNameLength+1)),
			wantErr:  fmt.Sprintf("may not exceed %d characters", automdm.MaxTeamNameLength),
		},
		{
			// Guards against regressing to byte-based length checks, which
			// would reject multibyte names that fit within the character cap.
			name:     "multibyte name at max character length is accepted",
			teamName: new(strings.Repeat("日", automdm.MaxTeamNameLength)),
			wantName: strings.Repeat("日", automdm.MaxTeamNameLength),
		},
		{
			name:     "emoji only is rejected",
			teamName: new("🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "repeated emoji is rejected",
			teamName: new("🚀🚀🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "punctuation only is rejected",
			teamName: new("!!!"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "dashes only is rejected",
			teamName: new("---"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK brackets only is rejected",
			teamName: new("「」"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK punctuation only is rejected",
			teamName: new("・・・"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "emoji separated by spaces is rejected",
			teamName: new("🚀 🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "combining mark only is rejected",
			teamName: new("\u093e"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "Devanagari name is accepted",
			teamName: new("राम"),
			wantName: "राम",
		},
		{
			name:     "CJK name is accepted",
			teamName: new("開発"),
			wantName: "開発",
		},
		{
			name:     "accented name is accepted",
			teamName: new("Ingeniería"),
			wantName: "Ingeniería",
		},
		{
			name:     "numbers only is accepted",
			teamName: new("2026"),
			wantName: "2026",
		},
		{
			name:     "name with an emoji is accepted",
			teamName: new("AutoMDM 🚀"),
			wantName: "AutoMDM 🚀",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := automdm.TeamPayload{Name: tc.teamName}

			team, err := svc.NewTeam(ctx, payload)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Nil(t, team)
			} else {
				require.NoError(t, err)
				require.NotNil(t, team)
				require.Equal(t, tc.wantName, team.Name)
			}
		})
	}
}

func TestModifyTeamNameValidation(t *testing.T) {
	ds := new(mock.Store)
	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid, Name: "existing-team"}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return team, nil
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	// A successful rename reconciles the team names copied into the app config.
	ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(context.Context, *uint) ([]string, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}

	adminUser := &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	ctx := test.UserContext(context.Background(), adminUser)

	testCases := []struct {
		name     string
		teamName *string
		wantErr  string
		wantName string
	}{
		{
			name:     "only spaces",
			teamName: ptr.String("     "),
			wantErr:  "may not be empty",
		},
		{
			name:     "only tabs",
			teamName: ptr.String("\t\t\t"),
			wantErr:  "may not be empty",
		},
		{
			name:     "only newlines",
			teamName: ptr.String("\n\n\n"),
			wantErr:  "may not be empty",
		},
		{
			name:     "only carriage returns",
			teamName: ptr.String("\r\r\r"),
			wantErr:  "may not be empty",
		},
		{
			name:     "mixed whitespace",
			teamName: ptr.String(" \t\n\r "),
			wantErr:  "may not be empty",
		},
		{
			name:     "empty string",
			teamName: ptr.String(""),
			wantErr:  "may not be empty",
		},
		{
			name:     "single space",
			teamName: ptr.String(" "),
			wantErr:  "may not be empty",
		},
		{
			name:     "nil name keeps existing name",
			wantName: "existing-team",
		},
		{
			name:     "valid name",
			teamName: ptr.String("new-name"),
			wantName: "new-name",
		},
		{
			name:     "leading spaces are trimmed",
			teamName: ptr.String("  new-name"),
			wantName: "new-name",
		},
		{
			name:     "trailing spaces are trimmed",
			teamName: ptr.String("new-name  "),
			wantName: "new-name",
		},
		{
			name:     "inner spaces preserved",
			teamName: ptr.String("my team"),
			wantName: "my team",
		},
		{
			name:     "name at max length is accepted",
			teamName: new(strings.Repeat("a", automdm.MaxTeamNameLength)),
			wantName: strings.Repeat("a", automdm.MaxTeamNameLength),
		},
		{
			name:     "name over max length is rejected",
			teamName: new(strings.Repeat("a", automdm.MaxTeamNameLength+1)),
			wantErr:  fmt.Sprintf("may not exceed %d characters", automdm.MaxTeamNameLength),
		},
		{
			name:     "multibyte name at max character length is accepted",
			teamName: new(strings.Repeat("日", automdm.MaxTeamNameLength)),
			wantName: strings.Repeat("日", automdm.MaxTeamNameLength),
		},
		{
			name:     "emoji only is rejected",
			teamName: new("🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "repeated emoji is rejected",
			teamName: new("🚀🚀🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "punctuation only is rejected",
			teamName: new("!!!"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "dashes only is rejected",
			teamName: new("---"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK brackets only is rejected",
			teamName: new("「」"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK punctuation only is rejected",
			teamName: new("・・・"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "emoji separated by spaces is rejected",
			teamName: new("🚀 🚀"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "combining mark only is rejected",
			teamName: new("\u093e"),
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "Devanagari name is accepted",
			teamName: new("राम"),
			wantName: "राम",
		},
		{
			name:     "CJK name is accepted",
			teamName: new("開発"),
			wantName: "開発",
		},
		{
			name:     "accented name is accepted",
			teamName: new("Ingeniería"),
			wantName: "Ingeniería",
		},
		{
			name:     "numbers only is accepted",
			teamName: new("2026"),
			wantName: "2026",
		},
		{
			name:     "name with an emoji is accepted",
			teamName: new("AutoMDM 🚀"),
			wantName: "AutoMDM 🚀",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := automdm.TeamPayload{Name: tc.teamName}

			team, err := svc.ModifyTeam(ctx, 1, payload)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Nil(t, team)
			} else {
				require.NoError(t, err)
				require.NotNil(t, team)
				require.Equal(t, tc.wantName, team.Name)
			}
		})
	}
}

func TestApplyTeamSpecsNameValidation(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, &notFoundError{}
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	svc := &Service{
		ds: ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}

	adminUser := &automdm.User{
		ID:         1,
		GlobalRole: ptr.String(automdm.RoleAdmin),
	}
	ctx := test.UserContext(context.Background(), adminUser)

	ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, newB bool, teamID *uint) (bool, error) {
		return true, nil
	}

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}
	svc.Service = mockSvc

	testCases := []struct {
		name     string
		teamName string
		wantErr  string
		wantName string
	}{
		{
			name:     "empty string",
			teamName: "",
			wantErr:  "may not be empty",
		},
		{
			name:     "only spaces",
			teamName: "     ",
			wantErr:  "may not be empty",
		},
		{
			name:     "only tabs",
			teamName: "\t\t\t",
			wantErr:  "may not be empty",
		},
		{
			name:     "only newlines",
			teamName: "\n\n\n",
			wantErr:  "may not be empty",
		},
		{
			name:     "only carriage returns",
			teamName: "\r\r\r",
			wantErr:  "may not be empty",
		},
		{
			name:     "mixed whitespace",
			teamName: " \t\n\r ",
			wantErr:  "may not be empty",
		},
		{
			name:     "single space",
			teamName: " ",
			wantErr:  "may not be empty",
		},
		{
			name:     "valid name",
			teamName: "Engineering",
			wantName: "Engineering",
		},
		{
			name:     "leading and trailing spaces are trimmed",
			teamName: "  Engineering  ",
			wantName: "Engineering",
		},
		{
			name:     "name at max length is accepted",
			teamName: strings.Repeat("a", automdm.MaxTeamNameLength),
			wantName: strings.Repeat("a", automdm.MaxTeamNameLength),
		},
		{
			name:     "name over max length is rejected",
			teamName: strings.Repeat("a", automdm.MaxTeamNameLength+1),
			wantErr:  fmt.Sprintf("may not exceed %d characters", automdm.MaxTeamNameLength),
		},
		{
			name:     "multibyte name at max character length is accepted",
			teamName: strings.Repeat("日", automdm.MaxTeamNameLength),
			wantName: strings.Repeat("日", automdm.MaxTeamNameLength),
		},
		{
			name:     "emoji only is rejected",
			teamName: "🚀",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "repeated emoji is rejected",
			teamName: "🚀🚀🚀",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "punctuation only is rejected",
			teamName: "!!!",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "dashes only is rejected",
			teamName: "---",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK brackets only is rejected",
			teamName: "「」",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "CJK punctuation only is rejected",
			teamName: "・・・",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "emoji separated by spaces is rejected",
			teamName: "🚀 🚀",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "combining mark only is rejected",
			teamName: "\u093e",
			wantErr:  "must include at least one letter or number",
		},
		{
			name:     "Devanagari name is accepted",
			teamName: "राम",
			wantName: "राम",
		},
		{
			name:     "CJK name is accepted",
			teamName: "開発",
			wantName: "開発",
		},
		{
			name:     "accented name is accepted",
			teamName: "Ingeniería",
			wantName: "Ingeniería",
		},
		{
			name:     "numbers only is accepted",
			teamName: "2026",
			wantName: "2026",
		},
		{
			name:     "name with an emoji is accepted",
			teamName: "AutoMDM 🚀",
			wantName: "AutoMDM 🚀",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
				{Name: tc.teamName},
			}, automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}})
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Contains(t, result, tc.wantName)
			}
		})
	}
}

// TestNewTeamCollationEqualConflict covers the case where the requested name
// collides with an existing team under MySQL's collation.
func TestNewTeamCollationEqualConflict(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		t.Fatalf("NewTeam should not be called when a conflict is detected")
		return nil, nil
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		require.Equal(t, uint(0), excludeID)
		return &automdm.Team{ID: 42, Name: "ABC"}, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}

	adminUser := &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}
	ctx := test.UserContext(context.Background(), adminUser)

	_, err = svc.NewTeam(ctx, automdm.TeamPayload{Name: new("abc")})
	require.Error(t, err)
	var conflict *automdm.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Contains(t, err.Error(), `"ABC"`)
	require.Contains(t, err.Error(), automdm.TeamNameConflictHint)
}

// TestModifyTeamCaseOnlyRenameAndConflict covers two ModifyTeam scenarios:
//  1. Case-only self-rename succeeds (the team is excluded from the conflict
//     check by id).
//  2. Rename into another team's name returns a ConflictError naming that
//     team.
func TestModifyTeamCaseOnlyRenameAndConflict(t *testing.T) {
	ds := new(mock.Store)
	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid, Name: "ABC"}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return team, nil
	}
	// A case-only rename is still a rename, so it reconciles the team names
	// copied into the app config.
	ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(context.Context, *uint) ([]string, error) {
		return nil, nil
	}
	ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
		return nil, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}

	adminUser := &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}
	ctx := test.UserContext(context.Background(), adminUser)

	t.Run("case-only self rename succeeds", func(t *testing.T) {
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			require.Equal(t, uint(5), excludeID)
			return nil, nil
		}

		team, err := svc.ModifyTeam(ctx, 5, automdm.TeamPayload{Name: new("abc")})
		require.NoError(t, err)
		require.NotNil(t, team)
		require.Equal(t, "abc", team.Name)
	})

	t.Run("rename into another team's name conflicts", func(t *testing.T) {
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			require.Equal(t, uint(5), excludeID)
			return &automdm.Team{ID: 6, Name: "def"}, nil
		}

		team, err := svc.ModifyTeam(ctx, 5, automdm.TeamPayload{Name: new("DEF")})
		require.Error(t, err)
		require.Nil(t, team)
		var conflict *automdm.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Contains(t, err.Error(), `"def"`)
		require.Contains(t, err.Error(), automdm.TeamNameConflictHint)
	})
}

func TestModifyTeamWebhookSettingsPartialUpdate(t *testing.T) {
	stored := func() automdm.TeamWebhookSettings {
		return automdm.TeamWebhookSettings{
			HostStatusWebhook: &automdm.HostStatusWebhookSettings{
				Enable: true, DestinationURL: "https://example.com/status", HostPercentage: 10, DaysCount: 2,
			},
			FailingPoliciesWebhook: automdm.FailingPoliciesWebhookSettings{
				Enable: true, DestinationURL: "https://example.com/policies", HostBatchSize: 5,
			},
			HostActivitiesWebhook: &automdm.HostActivitiesWebhookSettings{
				Enable: true, DestinationURL: "https://example.com/activities",
			},
		}
	}

	ds := new(mock.Store)
	var saved automdm.TeamWebhookSettings
	ds.TeamWithExtrasFunc = func(ctx context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid, Name: "team", Config: automdm.TeamConfig{WebhookSettings: stored()}}, nil
	}
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		saved = team.Config.WebhookSettings
		return team, nil
	}
	ds.DefaultTeamConfigFunc = func(ctx context.Context) (*automdm.TeamConfig, error) {
		return &automdm.TeamConfig{WebhookSettings: stored()}, nil
	}
	ds.SaveDefaultTeamConfigFunc = func(ctx context.Context, config *automdm.TeamConfig) error {
		saved = config.WebhookSettings
		return nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	svc := &Service{Service: &svcmock.Service{}, ds: ds, authz: authorizer}
	ctx := test.UserContext(t.Context(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	// Bodies match what the UI sends: each page only sends the webhook it manages.
	cases := []struct {
		name string
		body string
		want func(w *automdm.TeamWebhookSettings)
	}{
		{
			name: "host status only (team settings page)",
			body: `{"webhook_settings": {"host_status_webhook": {"enable_host_status_webhook": false, "destination_url": "https://example.com/status2", "host_percentage": 20, "days_count": 3}}}`,
			want: func(w *automdm.TeamWebhookSettings) {
				w.HostStatusWebhook = &automdm.HostStatusWebhookSettings{DestinationURL: "https://example.com/status2", HostPercentage: 20, DaysCount: 3}
			},
		},
		{
			name: "failing policies only (policies automations)",
			body: `{"webhook_settings": {"failing_policies_webhook": {"enable_failing_policies_webhook": true, "destination_url": "https://example.com/policies2", "host_batch_size": 0}}}`,
			want: func(w *automdm.TeamWebhookSettings) {
				w.FailingPoliciesWebhook = automdm.FailingPoliciesWebhookSettings{Enable: true, DestinationURL: "https://example.com/policies2"}
			},
		},
		{
			name: "host activities only (hosts page automations)",
			body: `{"webhook_settings": {"host_activities_webhook": {"enable_host_activities_webhook": false, "destination_url": ""}}}`,
			want: func(w *automdm.TeamWebhookSettings) {
				w.HostActivitiesWebhook = &automdm.HostActivitiesWebhookSettings{}
			},
		},
		{
			name: "empty webhook settings change nothing",
			body: `{"webhook_settings": {}}`,
			want: func(w *automdm.TeamWebhookSettings) {},
		},
		{
			name: "explicitly disabling failing policies",
			body: `{"webhook_settings": {"failing_policies_webhook": {"enable_failing_policies_webhook": false}}}`,
			want: func(w *automdm.TeamWebhookSettings) {
				w.FailingPoliciesWebhook = automdm.FailingPoliciesWebhookSettings{}
			},
		},
	}
	for _, teamID := range []uint{5, 0} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("team %d: %s", teamID, c.name), func(t *testing.T) {
				var payload automdm.TeamPayload
				require.NoError(t, json.Unmarshal([]byte(c.body), &payload))
				saved = automdm.TeamWebhookSettings{}

				_, err := svc.ModifyTeam(ctx, teamID, payload)
				require.NoError(t, err)

				want := stored()
				c.want(&want)
				require.Equal(t, want, saved)
			})
		}
	}
}

// TestApplyTeamSpecsCollationEqualConflict covers the three GitOps scenarios
// that were inconsistently handled:
//   - Single-team case-only rename should succeed.
//   - Cross-file conflict (new filename, colliding name) should return
//     ConflictError.
//   - Intra-batch conflict should be detected before any DB writes.
func TestApplyTeamSpecsCollationEqualConflict(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	adminUser := &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}
	ctx := test.UserContext(context.Background(), adminUser)

	newSvc := func() (*Service, *mock.Store) {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{}, nil
		}
		ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, newB bool, teamID *uint) (bool, error) {
			return true, nil
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return nil, &notFoundError{}
		}
		ds.TeamByFilenameFunc = func(ctx context.Context, filename string) (*automdm.Team, error) {
			return nil, &notFoundError{}
		}
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			return nil, nil
		}
		// A rename reconciles the team names copied into the app config.
		ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(context.Context, *uint) ([]string, error) {
			return nil, nil
		}
		ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
			return nil, nil
		}

		mockSvc := &svcmock.Service{}
		mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}
		svc := &Service{
			Service: mockSvc,
			ds:      ds,
			config: config.AutoMDMConfig{
				Server: config.ServerConfig{PrivateKey: "something"},
			},
			authz:  authorizer,
			logger: slog.New(slog.DiscardHandler),
		}
		return svc, ds
	}

	t.Run("case-only rename of existing team succeeds and persists new name", func(t *testing.T) {
		svc, ds := newSvc()
		filename := "abc.yml"
		existing := &automdm.Team{ID: 7, Name: "ABC", Filename: new(filename)}
		ds.TeamByFilenameFunc = func(ctx context.Context, f string) (*automdm.Team, error) {
			require.Equal(t, filename, f)
			return existing, nil
		}
		conflictCalls := 0
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			conflictCalls++
			require.Equal(t, uint(7), excludeID,
				"conflict check must exclude the team matched by filename so a case-only rename succeeds")
			return nil, nil
		}
		var savedTeam *automdm.Team
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			savedTeam = team
			return team, nil
		}

		// Run without DryRun so editTeamFromSpec actually rewrites team.Name
		// and SaveTeam is called — otherwise we'd only be asserting that the
		// conflict check didn't trip.
		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "abc", Filename: new(filename)},
		}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.Equal(t, 1, conflictCalls, "TeamConflictsWithName must be called once per spec")
		require.True(t, ds.SaveTeamFuncInvoked, "SaveTeam must be called to persist the rename")
		require.NotNil(t, savedTeam)
		require.Equal(t, "abc", savedTeam.Name, "rename must persist the spec's new case form")
		require.Equal(t, uint(7), savedTeam.ID, "rename must target the same team id")
	})

	t.Run("filename-matched rename into another team's name conflicts", func(t *testing.T) {
		// Regression: team "ABC" is managed by "abc.yml". User tries to
		// rename it to "DEF" via the same file, but another team "def"
		// already exists under a different file. This must 409.
		svc, ds := newSvc()
		existing := &automdm.Team{ID: 7, Name: "ABC", Filename: new("abc.yml")}
		ds.TeamByFilenameFunc = func(ctx context.Context, filename string) (*automdm.Team, error) {
			return existing, nil
		}
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			require.Equal(t, uint(7), excludeID, "must exclude the filename-matched team")
			return &automdm.Team{ID: 8, Name: "def"}, nil
		}
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			t.Fatalf("SaveTeam must not be called when a conflict is detected")
			return nil, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "DEF", Filename: new("abc.yml")},
		}, automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}})
		require.Error(t, err)
		var conflict *automdm.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Contains(t, err.Error(), `"def"`)
		require.Contains(t, err.Error(), automdm.TeamNameConflictHint)
	})

	t.Run("adopt existing team via new filename succeeds", func(t *testing.T) {
		// Regression for TestIntegrationsEnterpriseGitops: a spec with a new
		// filename that matches an existing team by name must adopt it
		// (possibly taking over management from another YAML file). The
		// pre-fix behavior was adoption; the fix must not break it.
		svc, ds := newSvc()
		existing := &automdm.Team{ID: 12, Name: "Adoptable", Filename: new("old.yml")}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return existing, nil
		}
		var savedTeam *automdm.Team
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			savedTeam = team
			return team, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "Adoptable", Filename: new("new.yml")},
		}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.True(t, ds.SaveTeamFuncInvoked, "SaveTeam must be called to adopt the team")
		require.NotNil(t, savedTeam)
		require.Equal(t, uint(12), savedTeam.ID)
		require.NotNil(t, savedTeam.Filename)
		require.Equal(t, "new.yml", *savedTeam.Filename, "adoption must set the new filename")
	})

	t.Run("intra-batch conflict short-circuits before any DB conflict check", func(t *testing.T) {
		svc, ds := newSvc()
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			t.Fatalf("TeamConflictsWithName must not be called when the pre-pass catches the conflict (got name=%q, excludeID=%d)", name, excludeID)
			return nil, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "ABC", Filename: new("foo.yml")},
			{Name: "abc", Filename: new("bar.yml")},
		}, automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}})
		require.Error(t, err)
		var conflict *automdm.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Contains(t, err.Error(), "foo.yml")
		require.Contains(t, err.Error(), "bar.yml")
		require.Contains(t, err.Error(), automdm.TeamNameConflictHint)
	})

	t.Run("intra-batch conflict ignores whitespace and punctuation", func(t *testing.T) {
		svc, ds := newSvc()
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			t.Fatalf("TeamConflictsWithName must not be called when the pre-pass catches the conflict (got name=%q, excludeID=%d)", name, excludeID)
			return nil, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "Work Stations"},
			{Name: "Servers"},
			{Name: "work-stations"},
		}, automdm.ApplyTeamSpecOptions{})
		var conflict *automdm.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Contains(t, err.Error(), `"Work Stations"`)
		require.Contains(t, err.Error(), `"work-stations"`)
		require.Contains(t, err.Error(), automdm.TeamNameConflictHint)
	})

	t.Run("new team that conflicts with an existing team is not created", func(t *testing.T) {
		for _, filename := range []*string{nil, new("work-stations.yml")} {
			for _, dryRun := range []bool{false, true} {
				svc, ds := newSvc()
				ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
					require.Equal(t, "Work-stations", name)
					require.Zero(t, excludeID)
					return &automdm.Team{ID: 3, Name: "Workstations"}, nil
				}
				ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
					t.Fatalf("NewTeam must not be called when a conflict is detected")
					return nil, nil
				}

				_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
					{Name: "Work-stations", Filename: filename},
				}, automdm.ApplyTeamSpecOptions{DryRun: dryRun})
				var conflict *automdm.ConflictError
				require.ErrorAs(t, err, &conflict)
				require.Contains(t, err.Error(), automdm.TeamNameConflictMessage("Work-stations", "Workstations"))
			}
		}
	})

	t.Run("adopting a team by name with a rename that conflicts with another team fails", func(t *testing.T) {
		// "Team 𝐁" matches the existing "Team 𝐀" under the collation, so the
		// spec adopts it. The new name then matches "Team B" by name key.
		svc, ds := newSvc()
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			return &automdm.Team{ID: 12, Name: "Team 𝐀"}, nil
		}
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			require.Equal(t, "Team 𝐁", name)
			require.Equal(t, uint(12), excludeID)
			return &automdm.Team{ID: 13, Name: "Team B"}, nil
		}
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			t.Fatalf("SaveTeam must not be called when a conflict is detected")
			return nil, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "Team 𝐁", Filename: new("team-b.yml")},
		}, automdm.ApplyTeamSpecOptions{})
		var conflict *automdm.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Contains(t, err.Error(), `"Team B"`)
	})

	t.Run("no-filename spec with collation-equal name preserves DB canonical name", func(t *testing.T) {
		// Regression: without a filename, a spec whose name is a case variant
		// of an existing team must NOT silently rename that team. The DB's
		// canonical form wins; users who want to rename must supply a
		// filename.
		svc, ds := newSvc()
		existing := &automdm.Team{ID: 11, Name: "Workstations"}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			// TeamByName is collation-aware in production, so "workstations"
			// matches "Workstations" here.
			return existing, nil
		}
		var savedTeam *automdm.Team
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			savedTeam = team
			return team, nil
		}

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "workstations"}, // no filename
		}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.True(t, ds.SaveTeamFuncInvoked)
		require.NotNil(t, savedTeam)
		require.Equal(t, "Workstations", savedTeam.Name,
			"no-filename spec must preserve the DB's canonical name, not silently case-rename it")
	})
}

// TestModifyTeamMDMEnableDiskEncryption covers the team-level PATCH endpoint
// validation for `mdm.enable_disk_encryption`. The flag governs both FileVault
// (Apple) and BitLocker (Windows), so the change must be allowed when either
// platform's MDM is configured. The Apple FileVault profile is created only
// when Apple MDM is configured.
func TestModifyTeamMDMEnableDiskEncryption(t *testing.T) {
	testCases := []struct {
		name           string
		appleEnabled   bool
		windowsEnabled bool
		wantErr        string
	}{
		{
			name:           "windows MDM only succeeds without invoking FileVault (issue #44194)",
			windowsEnabled: true,
		},
		{
			name:    "neither MDM platform configured rejects the change",
			wantErr: "mdm.enable_disk_encryption",
		},
		{
			name:         "apple MDM configured invokes FileVault profile creation",
			appleEnabled: true,
		},
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error {
		return nil
	}
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: ptr.String(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{
					EnabledAndConfigured:        tc.appleEnabled,
					WindowsEnabledAndConfigured: tc.windowsEnabled,
				}}, nil
			}
			ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
				return &automdm.Team{ID: tid, Name: "team-1"}, nil
			}
			var savedTeam *automdm.Team
			ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
				savedTeam = team
				return team, nil
			}
			// Wire the Apple FileVault mocks only when Apple MDM is configured;
			// a regression that drops the gate would call into them on a
			// Windows-only deployment and panic on the nil mock function.
			if tc.appleEnabled {
				ds.GetAllMDMConfigAssetsByNameFunc = func(_ context.Context, _ []automdm.MDMAssetName,
					_ sqlx.QueryerContext,
				) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
					return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
						automdm.MDMAssetCACert: {Value: []byte(testCert)},
					}, nil
				}
				// the reconciler reads the stored settings back, so serve what
				// SaveTeam just captured
				ds.TeamMDMConfigFunc = func(_ context.Context, _ uint) (*automdm.TeamMDM, error) {
					require.NotNil(t, savedTeam, "reconcile ran before the team was saved")
					mdm := savedTeam.Config.MDM
					return &mdm, nil
				}
				ds.UpsertMDMAppleAutoMDMConfigProfileFunc = func(_ context.Context, _ automdm.MDMAppleConfigProfile) error {
					return nil
				}
			}

			svc := &Service{
				Service: mockSvc,
				ds:      ds,
				config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
				authz:   authorizer,
			}

			payload := automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
				EnableDiskEncryption: optjson.SetBool(true),
			}}
			team, err := svc.ModifyTeam(ctx, 1, payload)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Nil(t, team)
				require.False(t, ds.SaveTeamFuncInvoked, "team should not have been saved")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, team)
			require.True(t, team.Config.MDM.EnableDiskEncryption)
			require.NotNil(t, savedTeam)
			require.True(t, savedTeam.Config.MDM.EnableDiskEncryption)
			require.Equal(t, tc.appleEnabled, ds.UpsertMDMAppleAutoMDMConfigProfileFuncInvoked,
				"FileVault profile write must match Apple MDM configuration")
		})
	}
}

func TestUpdateTeamMDMDiskEncryption(t *testing.T) {
	testCases := []struct {
		name           string
		mdmConfig      automdm.TeamMDM
		diskEncryption *bool
		requireTPMPIN  *bool
		expectedError  string
	}{
		{
			name: "try to disable disk encryption with TPM PIN enabled",
			// per-platform fields set to match the flat toggle, as the
			// unmarshal gap-fill guarantees for teams loaded from the DB
			mdmConfig: automdm.TeamMDM{
				EnableDiskEncryption: true,
				MacOSSettings: automdm.MacOSSettings{
					EnableDiskEncryption:          optjson.SetBool(true),
					EnableEscrowDiskEncryptionKey: optjson.SetBool(true),
				},
				WindowsSettings: automdm.WindowsSettings{
					EnableDiskEncryption: optjson.SetBool(true),
				},
				LinuxSettings: automdm.LinuxSettings{
					EnableEscrowDiskEncryptionKey: optjson.SetBool(true),
				},
				RequireBitLockerPIN: true,
			},
			diskEncryption: ptr.Bool(false),
			requireTPMPIN:  ptr.Bool(true),

			expectedError: automdm.CantDisableDiskEncryptionIfPINRequiredErrMsg,
		},
		{
			name: "try to enable disk encryption with TPM PIN enabled",
			mdmConfig: automdm.TeamMDM{
				EnableDiskEncryption: false,
				RequireBitLockerPIN:  false,
			},
			diskEncryption: ptr.Bool(false),
			requireTPMPIN:  ptr.Bool(true),
			expectedError:  automdm.CantEnablePINRequiredIfDiskEncryptionEnabled,
		},
		{
			name: "try to disable disk encryption with TPM PIN enabled when disk encryption prev enabled",
			mdmConfig: automdm.TeamMDM{
				EnableDiskEncryption: true,
				MacOSSettings: automdm.MacOSSettings{
					EnableDiskEncryption:          optjson.SetBool(true),
					EnableEscrowDiskEncryptionKey: optjson.SetBool(true),
				},
				WindowsSettings: automdm.WindowsSettings{
					EnableDiskEncryption: optjson.SetBool(true),
				},
				LinuxSettings: automdm.LinuxSettings{
					EnableEscrowDiskEncryptionKey: optjson.SetBool(true),
				},
				RequireBitLockerPIN: false,
			},
			diskEncryption: ptr.Bool(false),
			requireTPMPIN:  ptr.Bool(true),
			expectedError:  automdm.CantDisableDiskEncryptionIfPINRequiredErrMsg,
		},
	}

	ds := new(mock.Store)

	svc := &Service{
		ds: ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{
				PrivateKey: "something",
			},
		},
	}

	ctx := context.Background()

	for _, tC := range testCases {
		team := automdm.Team{
			Config: automdm.TeamConfig{
				MDM: tC.mdmConfig,
			},
		}

		// the flat toggle fans out to every per-platform setting, as
		// ResolvePerPlatform does for the endpoint payload
		changes := automdm.DiskEncryptionSettingsChanges{
			MacOSEnable:   tC.diskEncryption,
			MacOSEscrow:   tC.diskEncryption,
			WindowsEnable: tC.diskEncryption,
			LinuxEscrow:   tC.diskEncryption,
		}
		err := svc.updateTeamMDMDiskEncryption(
			ctx,
			&team,
			changes,
			tC.requireTPMPIN,
		)

		if tC.expectedError != "" {
			require.NotNil(t, err)
			require.True(
				t,
				strings.Contains(err.Error(), tC.expectedError),
				"Expected '%s' to contain '%s'",
				err.Error(), tC.expectedError)
		}
	}
}

func TestObfuscateSecrets(t *testing.T) {
	buildTeams := func(n int) []*automdm.Team {
		r := make([]*automdm.Team, 0, n)
		for i := 1; i <= n; i++ {
			r = append(r, &automdm.Team{
				ID: uint(i), //nolint:gosec // dismiss G115
				Secrets: []*automdm.EnrollSecret{
					{Secret: "abc"},
					{Secret: "123"},
				},
			})
		}
		return r
	}

	t.Run("no user", func(t *testing.T) {
		err := obfuscateSecrets(nil, nil)
		require.Error(t, err)
	})

	t.Run("no teams", func(t *testing.T) {
		user := automdm.User{}
		err := obfuscateSecrets(&user, nil)
		require.NoError(t, err)
	})

	t.Run("user is not a global observer", func(t *testing.T) {
		user := automdm.User{GlobalRole: ptr.String(automdm.RoleAdmin)}
		teams := buildTeams(3)

		err := obfuscateSecrets(&user, teams)
		require.NoError(t, err)

		for _, team := range teams {
			for _, s := range team.Secrets {
				require.NotEqual(t, automdm.MaskedPassword, s.Secret)
			}
		}
	})

	t.Run("user is global observer/observer+/technician", func(t *testing.T) {
		roles := []*string{
			ptr.String(automdm.RoleObserver),
			ptr.String(automdm.RoleObserverPlus),
			ptr.String(automdm.RoleTechnician),
		}
		for _, r := range roles {
			user := &automdm.User{GlobalRole: r}
			teams := buildTeams(3)

			err := obfuscateSecrets(user, teams)
			require.NoError(t, err)

			for _, team := range teams {
				for _, s := range team.Secrets {
					require.Equal(t, automdm.MaskedPassword, s.Secret)
				}
			}
		}
	})

	t.Run("user is global gitops", func(t *testing.T) {
		// gitops can write teams but is not allowed to read enroll secrets, so
		// the secrets must be masked even in write responses.
		user := &automdm.User{GlobalRole: new(automdm.RoleGitOps)}
		teams := buildTeams(3)

		err := obfuscateSecrets(user, teams)
		require.NoError(t, err)

		for _, team := range teams {
			for _, s := range team.Secrets {
				require.Equal(t, automdm.MaskedPassword, s.Secret)
			}
		}
	})

	t.Run("user is global maintainer", func(t *testing.T) {
		user := &automdm.User{GlobalRole: new(automdm.RoleMaintainer)}
		teams := buildTeams(3)

		err := obfuscateSecrets(user, teams)
		require.NoError(t, err)

		for _, team := range teams {
			for _, s := range team.Secrets {
				require.NotEqual(t, automdm.MaskedPassword, s.Secret)
			}
		}
	})

	t.Run("user is gitops/maintainer in some teams", func(t *testing.T) {
		teams := buildTeams(3)

		// Team gitops can modify the team but must not read its enroll secrets,
		// while team maintainer can. The user is not a member of team 0.
		user := &automdm.User{Teams: []automdm.UserTeam{
			{
				Team: *teams[1],
				Role: automdm.RoleGitOps,
			},
			{
				Team: *teams[2],
				Role: automdm.RoleMaintainer,
			},
		}}

		err := obfuscateSecrets(user, teams)
		require.NoError(t, err)

		for i, team := range teams {
			for _, s := range team.Secrets {
				// Only team 2 (maintainer) should be visible; team 0 (no
				// membership) and team 1 (gitops) must be masked.
				require.Equal(t, automdm.MaskedPassword == s.Secret, i == 0 || i == 1)
			}
		}
	})

	t.Run("user is observer/technician in some teams", func(t *testing.T) {
		teams := buildTeams(5)

		// Make user an observer in the 'even' teams
		user := &automdm.User{Teams: []automdm.UserTeam{
			{
				Team: *teams[1],
				Role: automdm.RoleObserver,
			},
			{
				Team: *teams[2],
				Role: automdm.RoleAdmin,
			},
			{
				Team: *teams[3],
				Role: automdm.RoleObserverPlus,
			},
			{
				Team: *teams[4],
				Role: automdm.RoleTechnician,
			},
		}}

		err := obfuscateSecrets(user, teams)
		require.NoError(t, err)

		for i, team := range teams {
			for _, s := range team.Secrets {
				require.Equal(t, automdm.MaskedPassword == s.Secret, i == 0 || i == 1 || i == 3 || i == 4)
			}
		}
	})
}

type bootstrapNotFoundError struct {
	msg string
}

func (e *bootstrapNotFoundError) Error() string {
	return e.msg
}

func (e *bootstrapNotFoundError) IsNotFound() bool {
	return true
}

func TestUpdateTeamMDMAppleSetupManualAgent(t *testing.T) {
	cases := []struct {
		Name             string
		Count            automdm.SetupExperienceCount
		Error            string
		BootstrapPackage string
		MDMSetupPayload  automdm.MDMAppleSetupPayload
	}{
		{
			Name:             "good case",
			BootstrapPackage: "package",
		},
		{
			Name: "no bootstrap package",
			Count: automdm.SetupExperienceCount{
				Installers: 0,
				VPP:        0,
				Scripts:    0,
			},
			Error: "bootstrap_package",
		},
		{
			Name: "installers exist",
			Count: automdm.SetupExperienceCount{
				Installers: 1,
				VPP:        0,
				Scripts:    0,
			},
			BootstrapPackage: "package",
			Error:            "disable setup experience software",
		},
		{
			Name:             "vpp apps exist",
			BootstrapPackage: "package",
			Count: automdm.SetupExperienceCount{
				VPP: 1,
			},
			Error: "disable setup experience software",
		},
		{
			Name: "script exists",
			Count: automdm.SetupExperienceCount{
				Scripts: 1,
			},
			BootstrapPackage: "package",
			Error:            "remove your setup experience script",
		},
	}

	ds := new(mock.Store)

	ds.SaveAppConfigFunc = func(ctx context.Context, info *automdm.AppConfig) error {
		return nil
	}

	ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		return &automdm.Team{}, nil
	}

	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	svc := &Service{
		ds: ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{
				PrivateKey: "something",
			},
		},
		authz: authorizer,
	}

	// Add admin user to context
	adminUser := &automdm.User{
		ID:         2,
		GlobalRole: ptr.String(automdm.RoleAdmin),
		Email:      "useradmin@example.com",
	}
	ctx := test.UserContext(context.Background(), adminUser)

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ds.GetMDMAppleBootstrapPackageMetaFunc = func(ctx context.Context, teamID uint) (*automdm.MDMAppleBootstrapPackage, error) {
				if tc.BootstrapPackage == "" {
					return nil, &bootstrapNotFoundError{msg: "bootstrap package not found"}
				}
				return &automdm.MDMAppleBootstrapPackage{
					Name: tc.BootstrapPackage,
				}, nil
			}

			ds.GetSetupExperienceCountFunc = func(ctx context.Context, platform string, teamID *uint) (*automdm.SetupExperienceCount, error) {
				return &tc.Count, nil
			}

			tm := &automdm.Team{}

			payload := automdm.MDMAppleSetupPayload{
				ManualAgentInstall: ptr.Bool(true),
			}

			err := svc.updateTeamMDMAppleSetup(ctx, tm, payload)
			if tc.Error == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.Error)
			}
		})
		t.Run(tc.Name+" no team", func(t *testing.T) {
			ds.GetSetupExperienceCountFunc = func(ctx context.Context, platform string, teamID *uint) (*automdm.SetupExperienceCount, error) {
				return &tc.Count, nil
			}

			ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{}, nil
			}

			payload := automdm.MDMAppleSetupPayload{
				ManualAgentInstall: ptr.Bool(true),
			}

			err := svc.updateAppConfigMDMAppleSetup(ctx, payload)
			if tc.Error == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.Error)
			}
		})

	}
}

func TestApplyTeamSpecsWindowsManagedLocalAccount(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	adminUser := &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)}
	ctx := test.UserContext(context.Background(), adminUser)

	const teamName = "Mobile"

	newSvc := func(t *testing.T, mdmConfigured bool) (*Service, *mock.Store, **automdm.Team) {
		ds := new(mock.Store)
		ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			// mdmConfigured=false simulates the bug condition: the cached
			// AppConfig still reports MDM as off even though it has actually
			// been enabled. The fix must let the edit proceed regardless.
			ac.MDM.AndroidEnabledAndConfigured = mdmConfigured
			ac.MDM.WindowsEnabledAndConfigured = mdmConfigured
			return ac, nil
		}
		ds.TeamByFilenameFunc = func(ctx context.Context, _ string) (*automdm.Team, error) {
			return nil, &notFoundError{}
		}
		ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
			// Existing team has no Android/Windows custom settings — the spec
			// is adding the first profile.
			return &automdm.Team{ID: 42, Name: name}, nil
		}
		ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
			return nil, nil
		}
		var saved *automdm.Team
		ds.SaveTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
			saved = team
			return team, nil
		}

		mockSvc := &svcmock.Service{}
		mockSvc.NewActivityFunc = func(ctx context.Context, _ *automdm.User, _ automdm.ActivityDetails) error {
			return nil
		}

		svc := &Service{
			Service: mockSvc,
			ds:      ds,
			config: config.AutoMDMConfig{
				Server: config.ServerConfig{PrivateKey: "something"},
			},
			authz:  authorizer,
			logger: slog.New(slog.DiscardHandler),
		}
		return svc, ds, &saved
	}

	t.Run("edit persists and disables the windows managed local account toggle", func(t *testing.T) {
		svc, ds, saved := newSvc(t, true)
		existing := &automdm.Team{ID: 42, Name: teamName}
		ds.TeamByNameFunc = func(context.Context, string) (*automdm.Team, error) { return existing, nil }
		spec := &automdm.TeamSpec{
			Name: teamName,
			MDM: automdm.TeamSpecMDM{
				MacOSSetup: automdm.MacOSSetupSpec{EnableManagedLocalAccountWindows: optjson.SetBool(true)},
			},
		}
		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{spec}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.True(t, (*saved).Config.MDM.MacOSSetup.EnableManagedLocalAccountWindows.Value)

		// an explicit false disables the managed local account again
		spec.MDM.MacOSSetup.EnableManagedLocalAccountWindows = optjson.SetBool(false)
		_, err = svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{spec}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)
		require.False(t, (*saved).Config.MDM.MacOSSetup.EnableManagedLocalAccountWindows.Value)
	})

}

// A GitOps dry run that turns on Windows MDM in the global file and creates a team with Windows setup settings
// validates the new team against that assumed state, since the global change isn't saved yet.
func TestApplyTeamSpecsCreateDryRunWindowsMDMAssumption(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil // Windows MDM is off
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) { return nil, &notFoundError{} }
	ds.TeamByFilenameFunc = func(ctx context.Context, name string) (*automdm.Team, error) { return nil, &notFoundError{} }
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, newB bool, teamID *uint) (bool, error) {
		return true, nil
	}
	svc := &Service{
		Service: &svcmock.Service{},
		ds:      ds,
		config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
		authz:   authorizer,
		logger:  slog.New(slog.DiscardHandler),
	}

	for _, tc := range []struct {
		name    string
		setup   automdm.MacOSSetup
		wantErr string
	}{
		{"managed local account", automdm.MacOSSetup{EnableManagedLocalAccountWindows: optjson.SetBool(true)},
			"setup_experience.windows_managed_local_account_enabled"},
		{"require all software", automdm.MacOSSetup{RequireAllSoftwareWindows: true}, "setup_experience.windows_require_all_software"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs := []*automdm.TeamSpec{{Name: "new team", MDM: automdm.TeamSpecMDM{MacOSSetup: automdm.MacOSSetupSpec{MacOSSetup: tc.setup}}}}

			_, err := svc.ApplyTeamSpecs(ctx, specs, automdm.ApplyTeamSpecOptions{DryRun: true})
			require.ErrorContains(t, err, tc.wantErr)

			_, err = svc.ApplyTeamSpecs(ctx, specs, automdm.ApplyTeamSpecOptions{
				DryRun:            true,
				DryRunAssumptions: &automdm.TeamSpecsDryRunAssumptions{WindowsEnabledAndConfigured: optjson.SetBool(true)},
			})
			require.NoError(t, err)
			require.False(t, ds.NewTeamFuncInvoked)
		})
	}
}

// TestModifyTeamMDMManagedLocalAccountRequiresMDM covers the MDM-off gates for both platform toggles, which the
// integration suite can't exercise since it always runs with MDM configured, plus the Windows managed local account toggle's
// persistence and activity.
func TestModifyTeamMDMManagedLocalAccountRequiresMDM(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	windowsMDMConfigured := false
	ds := new(mock.Store)
	ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{MDM: automdm.MDM{
			EnabledAndConfigured:        false,
			WindowsEnabledAndConfigured: windowsMDMConfigured,
		}}, nil
	}
	ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
		return &automdm.Team{ID: tid, Name: "team-1"}, nil
	}
	ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
		return team, nil
	}

	var activities []string
	mockSvc := &svcmock.Service{}
	// Reached via validateEndUserAuthenticationAndSetupAssistant when MacOSSetup is set.
	mockSvc.HasCustomSetupAssistantConfigurationWebURLFunc = func(context.Context, *uint) (bool, error) {
		return false, nil
	}
	mockSvc.NewActivityFunc = func(_ context.Context, _ *automdm.User, act automdm.ActivityDetails) error {
		switch a := act.(type) {
		case automdm.ActivityTypeEnabledManagedLocalAccount:
			activities = append(activities, a.ActivityName()+":"+a.Platform)
		case automdm.ActivityTypeDisabledManagedLocalAccount:
			activities = append(activities, a.ActivityName()+":"+a.Platform)
		}
		return nil
	}

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
		authz:   authorizer,
		logger:  slog.New(slog.DiscardHandler),
	}

	windowsPayload := automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
		MacOSSetup: &automdm.TeamPayloadMacOSSetup{
			EnableManagedLocalAccountWindows: optjson.SetBool(true),
		},
	}}

	t.Run("macOS enable admin account requires Apple MDM", func(t *testing.T) {
		_, err := svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSSetup: &automdm.TeamPayloadMacOSSetup{EnableManagedLocalAccount: optjson.SetBool(true)},
		}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "setup_experience.macos_managed_local_account_enabled")
		require.False(t, ds.SaveTeamFuncInvoked, "team should not have been saved")
	})

	t.Run("windows enable admin account requires Windows MDM", func(t *testing.T) {
		_, err := svc.ModifyTeam(ctx, 1, windowsPayload)
		require.Error(t, err)
		require.Contains(t, err.Error(), "setup_experience.windows_managed_local_account_enabled")
		require.False(t, ds.SaveTeamFuncInvoked, "team should not have been saved")
	})

	t.Run("windows enable admin toggle persists and fires activity", func(t *testing.T) {
		windowsMDMConfigured = true
		team, err := svc.ModifyTeam(ctx, 1, windowsPayload)
		require.NoError(t, err)
		require.True(t, team.Config.MDM.MacOSSetup.EnableManagedLocalAccountWindows.Value)
		require.False(t, team.Config.MDM.MacOSSetup.EnableManagedLocalAccount.Value)
		require.Equal(t, []string{"enabled_managed_local_account:windows"}, activities)
	})

	// The Windows setting lives under setup_experience, so a Windows-only change must not trip the Apple-only checks.
	t.Run("windows-only change ignores the custom setup assistant and a stored macOS pair", func(t *testing.T) {
		windowsMDMConfigured = true
		mockSvc.HasCustomSetupAssistantConfigurationWebURLFunc = func(context.Context, *uint) (bool, error) {
			return true, nil
		}
		ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
			tm := &automdm.Team{ID: tid, Name: "team-1"}
			tm.Config.MDM.MacOSSetup.EnableManagedLocalAccount = optjson.SetBool(false)
			tm.Config.MDM.MacOSSetup.EndUserLocalAccountType = optjson.SetString("none")
			return tm, nil
		}

		team, err := svc.ModifyTeam(ctx, 1, windowsPayload)
		require.NoError(t, err)
		require.True(t, team.Config.MDM.MacOSSetup.EnableManagedLocalAccountWindows.Value)

		// Both checks still apply when the request touches what they guard.
		_, err = svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSSetup: &automdm.TeamPayloadMacOSSetup{EndUserLocalAccountType: optjson.SetString("standard")},
		}})
		require.ErrorContains(t, err, "macos_managed_local_account_enabled is required")
		ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
			ac := &automdm.AppConfig{}
			ac.MDM.EndUserAuthentication.EntityID = "idp"
			return ac, nil
		}
		_, err = svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSSetup: &automdm.TeamPayloadMacOSSetup{EnableEndUserAuthentication: optjson.SetBool(true)},
		}})
		require.ErrorContains(t, err, automdm.EndUserAuthDEPWebURLConfiguredErrMsg)
	})
}

func TestDeleteTeamWindowsEnrollmentDefaultTeam(t *testing.T) {
	deletedTeamID, otherTeamID := uint(42), uint(43)

	testCases := []struct {
		name             string
		defaultAutoMDMID *uint
		wantCleared      bool
	}{
		{name: "deleted team is the configured default", defaultAutoMDMID: &deletedTeamID, wantCleared: true},
		{name: "another team is the configured default", defaultAutoMDMID: &otherTeamID},
		{name: "no default configured"},
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ds := new(mock.Store)
			ds.TeamLiteFunc = func(_ context.Context, tid uint) (*automdm.TeamLite, error) {
				return &automdm.TeamLite{ID: tid, Name: "team-1"}, nil
			}
			ds.ListHostsFunc = func(context.Context, automdm.TeamFilter, automdm.HostListOptions) ([]*automdm.Host, error) {
				return nil, nil
			}
			ds.GetCertificateTemplatesByTeamIDFunc = func(context.Context, uint, automdm.ListOptions) (
				[]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error,
			) {
				return nil, nil, nil
			}
			// Deleting a team also scrubs its name from the app config copies.
			ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{}, nil
			}
			ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(context.Context, *uint) ([]string, error) {
				return nil, nil
			}
			ds.ListVPPTokensFunc = func(context.Context) ([]*automdm.VPPTokenDB, error) {
				return nil, nil
			}
			ds.GetWindowsEnrollmentDefaultTeamFunc = func(context.Context) (*uint, string, error) {
				return tc.defaultAutoMDMID, "default-team", nil
			}
			var clearedTo *uint
			ds.SetWindowsEnrollmentDefaultTeamFunc = func(_ context.Context, automdmID *uint) error {
				clearedTo = automdmID
				return nil
			}
			ds.DeleteTeamFunc = func(context.Context, uint) error { return nil }

			var activities []string
			mockSvc := &svcmock.Service{}
			mockSvc.NewActivityFunc = func(_ context.Context, _ *automdm.User, act automdm.ActivityDetails) error {
				if a, ok := act.(automdm.ActivityTypeEditedWindowsEnrollmentDefaultTeam); ok {
					require.Nil(t, a.AutoMDMID, "cleared default must not name a team")
					require.Nil(t, a.AutoMDMName, "cleared default must not name a team")
				}
				activities = append(activities, act.ActivityName())
				return nil
			}

			svc := &Service{
				Service: mockSvc,
				ds:      ds,
				authz:   authorizer,
				logger:  slog.New(slog.DiscardHandler),
			}

			require.NoError(t, svc.DeleteTeam(ctx, deletedTeamID))

			// The deleted team activity always fires; the enrollment one only when the default was actually cleared.
			require.Contains(t, activities, automdm.ActivityTypeDeletedTeam{}.ActivityName())
			clearedActivity := automdm.ActivityTypeEditedWindowsEnrollmentDefaultTeam{}.ActivityName()
			if tc.wantCleared {
				require.True(t, ds.SetWindowsEnrollmentDefaultTeamFuncInvoked)
				require.Nil(t, clearedTo, "default team should be cleared, not reassigned")
				require.Contains(t, activities, clearedActivity)
			} else {
				require.False(t, ds.SetWindowsEnrollmentDefaultTeamFuncInvoked)
				require.NotContains(t, activities, clearedActivity)
			}
		})
	}
}

func TestModifyTeamOSUpdatesDeadlineDays(t *testing.T) {
	// A deadline_days-only edit must be treated as a change: the setting has to be
	// stored and the OS update declaration regenerated. Before deadline_days was
	// part of the change detection, both were silently skipped.
	testCases := []struct {
		name         string
		storedDays   optjson.Int
		payloadDays  optjson.Int
		wantSaved    int
		wantRedeploy bool
	}{
		{
			name:         "deadline_days changed",
			storedDays:   optjson.SetInt(14),
			payloadDays:  optjson.SetInt(21),
			wantSaved:    21,
			wantRedeploy: true,
		},
		{
			name:         "deadline_days set from unset",
			storedDays:   optjson.Int{},
			payloadDays:  optjson.SetInt(14),
			wantSaved:    14,
			wantRedeploy: true,
		},
		{
			name:         "deadline_days unchanged",
			storedDays:   optjson.SetInt(14),
			payloadDays:  optjson.SetInt(14),
			wantSaved:    14,
			wantRedeploy: false,
		},
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotActivities []automdm.ActivityDetails
			mockSvc := &svcmock.Service{}
			mockSvc.NewActivityFunc = func(_ context.Context, _ *automdm.User, a automdm.ActivityDetails) error {
				gotActivities = append(gotActivities, a)
				return nil
			}

			ds := new(mock.Store)
			ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
			}
			ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
				return &automdm.Team{ID: tid, Name: "team-1", Config: automdm.TeamConfig{
					MDM: automdm.TeamMDM{
						MacOSUpdates: automdm.AppleOSUpdateSettings{
							MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
							DeadlineDays:   tc.storedDays,
						},
					},
				}}, nil
			}
			var savedTeam *automdm.Team
			ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
				savedTeam = team
				return team, nil
			}
			ds.HasAppleUpdateConfigProfileConfiguredFunc = func(_ context.Context, teamID uint) (bool, error) {
				return false, nil
			}
			ds.LabelIDsByNameFunc = func(_ context.Context, names []string, _ automdm.TeamFilter) (map[string]uint, error) {
				ids := make(map[string]uint, len(names))
				for i, name := range names {
					ids[name] = uint(i + 1) //nolint:gosec
				}
				return ids, nil
			}
			var gotDecl *automdm.MDMAppleDeclaration
			var gotVars []automdm.AutoMDMVarName
			ds.SetOrUpdateMDMAppleDeclarationFunc = func(_ context.Context, decl *automdm.MDMAppleDeclaration,
				usesAutoMDMVars []automdm.AutoMDMVarName, activationAction automdm.MDMAppleActivationAction,
			) (*automdm.MDMAppleDeclaration, error) {
				gotDecl = decl
				gotVars = usesAutoMDMVars
				decl.DeclarationUUID = "decl-uuid"
				return decl, nil
			}

			svc := &Service{
				Service: mockSvc,
				ds:      ds,
				config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
				authz:   authorizer,
			}

			payload := automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
				MacOSUpdates: &automdm.AppleOSUpdateSettings{
					MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
					DeadlineDays:   tc.payloadDays,
				},
			}}
			team, err := svc.ModifyTeam(ctx, 1, payload)
			require.NoError(t, err)
			require.NotNil(t, team)

			// The outer Set guard also controls whether the value is stored at all.
			require.NotNil(t, savedTeam)
			require.Equal(t, tc.wantSaved, savedTeam.Config.MDM.MacOSUpdates.DeadlineDays.Value)

			require.Equal(t, tc.wantRedeploy, ds.SetOrUpdateMDMAppleDeclarationFuncInvoked,
				"declaration regeneration must follow the change detection")
			if tc.wantRedeploy {
				require.NotNil(t, gotDecl)
				require.Contains(t, string(gotDecl.RawJSON), "$AUTOMDM_VAR_HOST_TARGET_OS_VERSION")
				require.Len(t, gotVars, 2)
			}

			// The activity feed renders "updated macOS version to latest" from
			// minimum_version, so the payload has to carry the sentinel through.
			// Deadline stays empty in latest mode, which is what makes the
			// renderer drop its "(deadline: ...)" clause.
			var osUpdateActivities []automdm.ActivityTypeEditedMacOSMinVersion
			for _, a := range gotActivities {
				if edited, ok := a.(automdm.ActivityTypeEditedMacOSMinVersion); ok {
					osUpdateActivities = append(osUpdateActivities, edited)
				}
			}
			if !tc.wantRedeploy {
				require.Empty(t, osUpdateActivities, "an unchanged setting must not emit an activity")
				return
			}
			require.Len(t, osUpdateActivities, 1)
			require.Equal(t, automdm.AppleOSUpdateLatestVersion, osUpdateActivities[0].MinimumVersion)
			require.Empty(t, osUpdateActivities[0].Deadline)
			require.NotNil(t, osUpdateActivities[0].TeamID)
			require.Equal(t, uint(1), *osUpdateActivities[0].TeamID)
		})
	}
}

// ModifyTeam validates the incoming payload and then replaces the whole
// AppleOSUpdateSettings struct, so a stored deadline field can't leak into the
// validated value. ModifyAppConfig merges the payload over the stored config
// instead, which is why it needed clearStaleAppleOSUpdateDeadline and this
// doesn't. These cases lock that in for both directions: a sparse PATCH that
// switches mode must succeed and must not persist the outgoing mode's deadline.
func TestModifyTeamSwitchingOSUpdateModes(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	storedLatest := automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
		DeadlineDays:   optjson.SetInt(14),
	}

	setup := func(t *testing.T, stored automdm.AppleOSUpdateSettings) (*Service, func() *automdm.Team) {
		// ModifyTeam checks minimum_version against GDMF, so serve Apple's asset
		// list from the local fixture rather than reaching out to Apple.
		mdmtest.StartNewAppleGDMFTestServer(t)

		mockSvc := &svcmock.Service{}
		mockSvc.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error {
			return nil
		}

		ds := new(mock.Store)
		ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
			return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
		}
		ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
			return &automdm.Team{ID: tid, Name: "team-1", Config: automdm.TeamConfig{
				MDM: automdm.TeamMDM{MacOSUpdates: stored},
			}}, nil
		}
		var savedTeam *automdm.Team
		ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
			savedTeam = team
			return team, nil
		}
		ds.HasAppleUpdateConfigProfileConfiguredFunc = func(context.Context, uint) (bool, error) {
			return false, nil
		}
		ds.LabelIDsByNameFunc = func(_ context.Context, names []string, _ automdm.TeamFilter) (map[string]uint, error) {
			ids := make(map[string]uint, len(names))
			for i, name := range names {
				ids[name] = uint(i + 1) //nolint:gosec // G115: small test values
			}
			return ids, nil
		}
		ds.SetOrUpdateMDMAppleDeclarationFunc = func(_ context.Context, decl *automdm.MDMAppleDeclaration,
			_ []automdm.AutoMDMVarName, activationAction automdm.MDMAppleActivationAction,
		) (*automdm.MDMAppleDeclaration, error) {
			decl.DeclarationUUID = "decl-uuid"
			return decl, nil
		}
		ds.DeleteMDMAppleDeclarationByNameFunc = func(context.Context, *uint, string) error {
			return nil
		}

		return &Service{
			Service: mockSvc,
			ds:      ds,
			config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
			authz:   authorizer,
		}, func() *automdm.Team { return savedTeam }
	}

	t.Run("switching to a specific version", func(t *testing.T) {
		svc, saved := setup(t, storedLatest)

		// deadline_days is deliberately absent, as a sparse PATCH would leave it.
		_, err := svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSUpdates: &automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString("14.6.1"),
				Deadline:       optjson.SetString("2026-09-01"),
			},
		}})
		require.NoError(t, err)

		require.NotNil(t, saved())
		require.Equal(t, "14.6.1", saved().Config.MDM.MacOSUpdates.MinimumVersion.Value)
		require.False(t, saved().Config.MDM.MacOSUpdates.DeadlineDays.Valid,
			"the stored deadline_days must not survive the mode change")
	})

	t.Run("clearing enforcement entirely", func(t *testing.T) {
		svc, saved := setup(t, storedLatest)

		_, err := svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSUpdates: &automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(""),
				Deadline:       optjson.SetString(""),
			},
		}})
		require.NoError(t, err)

		require.NotNil(t, saved())
		require.Empty(t, saved().Config.MDM.MacOSUpdates.MinimumVersion.Value)
		require.False(t, saved().Config.MDM.MacOSUpdates.DeadlineDays.Valid)
	})

	t.Run("switching into latest mode from a specific version", func(t *testing.T) {
		// the mirror direction: a stored deadline is the stale field here, and the
		// wholesale replace has to drop it just the same.
		svc, saved := setup(t, automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.SetString("14.6.1"),
			Deadline:       optjson.SetString("2026-09-01"),
		})

		// deadline is deliberately absent, as a sparse PATCH would leave it.
		_, err := svc.ModifyTeam(ctx, 1, automdm.TeamPayload{MDM: &automdm.TeamPayloadMDM{
			MacOSUpdates: &automdm.AppleOSUpdateSettings{
				MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
				DeadlineDays:   optjson.SetInt(14),
			},
		}})
		require.NoError(t, err)

		require.NotNil(t, saved())
		require.Equal(t, automdm.AppleOSUpdateLatestVersion, saved().Config.MDM.MacOSUpdates.MinimumVersion.Value)
		require.Equal(t, 14, saved().Config.MDM.MacOSUpdates.DeadlineDays.Value)
		require.Empty(t, saved().Config.MDM.MacOSUpdates.Deadline.Value,
			"the stored deadline must not survive the mode change")
	})
}

func TestApplyTeamSpecsOSUpdatesValidation(t *testing.T) {
	// GitOps applies team settings through editTeamFromSpec, which validates each
	// Apple platform's OS update settings. All three must reject invalid settings,
	// keyed by the platform that is at fault.
	latest := func(days optjson.Int) automdm.AppleOSUpdateSettings {
		return automdm.AppleOSUpdateSettings{
			MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion),
			DeadlineDays:   days,
		}
	}
	valid := latest(optjson.SetInt(14))
	missingDays := latest(optjson.Int{})

	testCases := []struct {
		name    string
		mdm     automdm.TeamSpecMDM
		wantErr string
	}{
		{
			name: "all platforms valid",
			mdm:  automdm.TeamSpecMDM{MacOSUpdates: valid, IOSUpdates: valid, IPadOSUpdates: valid},
		},
		{
			name:    "macos missing deadline_days",
			mdm:     automdm.TeamSpecMDM{MacOSUpdates: missingDays},
			wantErr: "macos_updates",
		},
		{
			name:    "ios missing deadline_days",
			mdm:     automdm.TeamSpecMDM{IOSUpdates: missingDays},
			wantErr: "ios_updates",
		},
		{
			name:    "ipados missing deadline_days",
			mdm:     automdm.TeamSpecMDM{IPadOSUpdates: missingDays},
			wantErr: "ipados_updates",
		},
		{
			name:    "macos deadline with latest",
			mdm:     automdm.TeamSpecMDM{MacOSUpdates: automdm.AppleOSUpdateSettings{MinimumVersion: optjson.SetString(automdm.AppleOSUpdateLatestVersion), Deadline: optjson.SetString("2026-09-01"), DeadlineDays: optjson.SetInt(14)}},
			wantErr: "macos_updates",
		},
		{
			// Not a "latest" case: a half-configured block was accepted before iOS
			// was validated here, then enforced nothing because Configured() needs
			// both fields. Existing automdm files like this now fail the apply.
			name:    "ios version without deadline",
			mdm:     automdm.TeamSpecMDM{IOSUpdates: automdm.AppleOSUpdateSettings{MinimumVersion: optjson.SetString("17.5")}},
			wantErr: "ios_updates",
		},
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(context.Background(),
		&automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockSvc := &svcmock.Service{}
			mockSvc.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error {
				return nil
			}

			ds := new(mock.Store)
			ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
				return &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}, nil
			}
			ds.TeamByNameFunc = func(_ context.Context, name string) (*automdm.Team, error) {
				return &automdm.Team{ID: 1, Name: name}, nil
			}
			ds.TeamConflictsWithNameFunc = func(context.Context, string, uint) (*automdm.Team, error) {
				return nil, nil
			}
			ds.IsEnrollSecretAvailableFunc = func(context.Context, string, bool, *uint) (bool, error) {
				return true, nil
			}
			ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
				return team, nil
			}
			ds.HasAppleUpdateConfigProfileConfiguredFunc = func(context.Context, uint) (bool, error) {
				return false, nil
			}
			ds.LabelIDsByNameFunc = func(_ context.Context, names []string, _ automdm.TeamFilter) (map[string]uint, error) {
				ids := make(map[string]uint, len(names))
				for i, name := range names {
					ids[name] = uint(i + 1) //nolint:gosec
				}
				return ids, nil
			}
			ds.SetOrUpdateMDMAppleDeclarationFunc = func(_ context.Context, decl *automdm.MDMAppleDeclaration,
				_ []automdm.AutoMDMVarName, activationAction automdm.MDMAppleActivationAction,
			) (*automdm.MDMAppleDeclaration, error) {
				decl.DeclarationUUID = "decl-uuid"
				return decl, nil
			}

			svc := &Service{
				Service: mockSvc,
				ds:      ds,
				config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
				authz:   authorizer,
			}

			_, err := svc.ApplyTeamSpecs(ctx,
				[]*automdm.TeamSpec{{Name: "team-1", MDM: tc.mdm}},
				automdm.ApplyTeamSpecOptions{})

			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.ErrorContains(t, err, tc.wantErr,
				"the error must name the platform whose settings are invalid")
			require.False(t, ds.SaveTeamFuncInvoked, "an invalid spec must not be persisted")
		})
	}
}

// GitOps creating a brand-new team has to persist the Apple OS update settings
// the same way editing an existing one does; they were previously dropped.
func TestApplyTeamSpecsCreateAppliesAppleOSUpdates(t *testing.T) {
	ds := new(mock.Store)
	ds.AppConfigFunc = func(ctx context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.TeamByNameFunc = func(ctx context.Context, name string) (*automdm.Team, error) {
		return nil, &notFoundError{}
	}
	ds.TeamConflictsWithNameFunc = func(ctx context.Context, name string, excludeID uint) (*automdm.Team, error) {
		return nil, nil
	}
	ds.IsEnrollSecretAvailableFunc = func(ctx context.Context, secret string, newB bool, teamID *uint) (bool, error) {
		return true, nil
	}

	var created *automdm.Team
	ds.NewTeamFunc = func(ctx context.Context, team *automdm.Team) (*automdm.Team, error) {
		created = team
		team.ID = 1
		return team, nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	svc := &Service{
		ds: ds,
		config: config.AutoMDMConfig{
			Server: config.ServerConfig{PrivateKey: "something"},
		},
		authz: authorizer,
	}
	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
		return nil
	}
	svc.Service = mockSvc

	ctx := test.UserContext(t.Context(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	spec := &automdm.TeamSpec{Name: "Engineering"}
	spec.MDM.MacOSUpdates = automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("15.0"),
		Deadline:       optjson.SetString("2026-09-01"),
	}
	spec.MDM.IOSUpdates = automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("18.1"),
		Deadline:       optjson.SetString("2026-09-02"),
	}
	spec.MDM.IPadOSUpdates = automdm.AppleOSUpdateSettings{
		MinimumVersion: optjson.SetString("18.2"),
		Deadline:       optjson.SetString("2026-09-03"),
	}

	_, err = svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{spec}, automdm.ApplyTeamSpecOptions{})
	require.NoError(t, err)
	require.True(t, ds.NewTeamFuncInvoked)
	require.NotNil(t, created)

	// macOS already worked; it's here so a regression that drops all of them is
	// distinguishable from one that drops only the iOS/iPadOS pair.
	require.Equal(t, "15.0", created.Config.MDM.MacOSUpdates.MinimumVersion.Value)
	require.Equal(t, "18.1", created.Config.MDM.IOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2026-09-02", created.Config.MDM.IOSUpdates.Deadline.Value)
	require.Equal(t, "18.2", created.Config.MDM.IPadOSUpdates.MinimumVersion.Value)
	require.Equal(t, "2026-09-03", created.Config.MDM.IPadOSUpdates.Deadline.Value)
}

// staleTeamNamesFixture is the state a rename or a delete has to reconcile: the
// team names copied into mdm.apple_business, plus the abm_tokens rows that are
// the source of truth for which of those entries may be touched.
type staleTeamNamesFixture struct {
	abmStored []automdm.MDMAppleABMAssignmentInfo
	abmOrgs   []string // ABM tokens defaulting to this team
}

// newStaleTeamNamesService wires a service over f, and returns a live count of
// app config writes.
func newStaleTeamNamesService(t *testing.T, teamID uint, f staleTeamNamesFixture) (*Service, *mock.Store, *automdm.AppConfig, *int) {
	appCfg := &automdm.AppConfig{}
	appCfg.MDM.AppleBusinessManager = optjson.SetSlice(f.abmStored)

	var saves int
	ds := new(mock.Store)
	ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) { return appCfg, nil }
	ds.TeamConflictsWithNameFunc = func(context.Context, string, uint) (*automdm.Team, error) { return nil, nil }
	ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) { return team, nil }
	ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFunc = func(_ context.Context, tid *uint) ([]string, error) {
		require.NotNil(t, tid)
		require.Equal(t, teamID, *tid, "must look up ABM defaults for this team")
		return f.abmOrgs, nil
	}
	ds.SaveAppConfigFunc = func(context.Context, *automdm.AppConfig) error {
		saves++
		return nil
	}

	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)

	mockSvc := &svcmock.Service{}
	mockSvc.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error { return nil }

	return &Service{
		Service: mockSvc,
		ds:      ds,
		config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
		authz:   authorizer,
		logger:  slog.New(slog.DiscardHandler),
	}, ds, appCfg, &saves
}

func TestApplyTeamSpecsRenameUpdatesStaleAppConfigTeamNames(t *testing.T) {
	ctx := test.UserContext(context.Background(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	const filename = "workstations.yml"

	newSvc := func() (*Service, *mock.Store, *automdm.AppConfig, *int) {
		svc, ds, appCfg, saves := newStaleTeamNamesService(t, 7, staleTeamNamesFixture{
			abmStored: []automdm.MDMAppleABMAssignmentInfo{
				{OrganizationName: "Acme Inc", MacOSTeam: "Workstations", IpadOSTeam: "Tablets"},
			},
			abmOrgs: []string{"Acme Inc"},
		})
		ds.IsEnrollSecretAvailableFunc = func(context.Context, string, bool, *uint) (bool, error) { return true, nil }
		ds.TeamByNameFunc = func(context.Context, string) (*automdm.Team, error) { return nil, &notFoundError{} }
		ds.TeamByFilenameFunc = func(context.Context, string) (*automdm.Team, error) {
			return &automdm.Team{ID: 7, Name: "Workstations", Filename: new(filename)}, nil
		}
		return svc, ds, appCfg, saves
	}

	t.Run("rename via filename-matched spec rewrites the ABM entries", func(t *testing.T) {
		svc, _, appCfg, saves := newSvc()

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "Laptops", Filename: new(filename)},
		}, automdm.ApplyTeamSpecOptions{})
		require.NoError(t, err)

		require.Equal(t, 1, *saves)
		require.Equal(t, []automdm.MDMAppleABMAssignmentInfo{
			{OrganizationName: "Acme Inc", MacOSTeam: "Laptops", IpadOSTeam: "Tablets"},
		}, appCfg.MDM.AppleBusinessManager.Value)
	})

	t.Run("dry run does not touch the ABM config", func(t *testing.T) {
		svc, ds, appCfg, saves := newSvc()

		_, err := svc.ApplyTeamSpecs(ctx, []*automdm.TeamSpec{
			{Name: "Laptops", Filename: new(filename)},
		}, automdm.ApplyTeamSpecOptions{ApplySpecOptions: automdm.ApplySpecOptions{DryRun: true}})
		require.NoError(t, err)

		require.False(t, ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFuncInvoked)
		require.Zero(t, *saves)
		require.Equal(t, "Workstations", appCfg.MDM.AppleBusinessManager.Value[0].MacOSTeam)
	})
}

func TestModifyTeamRenameUpdatesStaleAppConfigTeamNames(t *testing.T) {
	const teamID = uint(5)

	testCases := []struct {
		name       string
		newName    string
		fixture    staleTeamNamesFixture
		wantLookup bool
		wantSaves  int
		wantABM    []automdm.MDMAppleABMAssignmentInfo
	}{
		{
			name:    "renames the team in the ABM entries",
			newName: "Laptops",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{
					{OrganizationName: "Acme Inc", MacOSTeam: "Workstations", IOSTeam: "Phones"},
				},
				abmOrgs: []string{"Acme Inc"},
			},
			wantLookup: true,
			wantSaves:  1,
			wantABM: []automdm.MDMAppleABMAssignmentInfo{
				{OrganizationName: "Acme Inc", MacOSTeam: "Laptops", IOSTeam: "Phones"},
			},
		},
		{
			name:    "other ABM tokens are left alone",
			newName: "Laptops",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{
					{OrganizationName: "Acme Inc", MacOSTeam: "Workstations"},
					{OrganizationName: "Beta Corp", MacOSTeam: "Workstations"},
				},
				abmOrgs: []string{"Acme Inc"},
			},
			wantLookup: true,
			wantSaves:  1,
			wantABM: []automdm.MDMAppleABMAssignmentInfo{
				{OrganizationName: "Acme Inc", MacOSTeam: "Laptops"},
				{OrganizationName: "Beta Corp", MacOSTeam: "Workstations"},
			},
		},
		{
			name:    "team isn't referenced",
			newName: "Laptops",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Servers"}},
			},
			wantLookup: true,
			wantABM:    []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Servers"}},
		},
		{
			name:    "same name is not a rename",
			newName: "Workstations",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Workstations"}},
				abmOrgs:   []string{"Acme Inc"},
			},
			wantABM: []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Workstations"}},
		},
	}

	ctx := test.UserContext(context.Background(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc, ds, appCfg, saves := newStaleTeamNamesService(t, teamID, tc.fixture)
			ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
				return &automdm.Team{ID: tid, Name: "Workstations"}, nil
			}

			team, err := svc.ModifyTeam(ctx, teamID, automdm.TeamPayload{Name: new(tc.newName)})
			require.NoError(t, err)
			require.Equal(t, tc.newName, team.Name)

			require.Equal(t, tc.wantLookup, ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFuncInvoked,
				"a no-op rename must not query ABM defaults")
			require.Equal(t, tc.wantSaves, *saves)
			require.Equal(t, tc.wantABM, appCfg.MDM.AppleBusinessManager.Value)
		})
	}
}

func TestDeleteTeamCleansStaleAppConfigTeamNames(t *testing.T) {
	const teamID = uint(42)

	testCases := []struct {
		name      string
		fixture   staleTeamNamesFixture
		wantSaves int
		wantABM   []automdm.MDMAppleABMAssignmentInfo
	}{
		{
			name: "clears the team from the ABM entries",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{
					{OrganizationName: "Acme Inc", MacOSTeam: "Workstations", IOSTeam: "Phones"},
				},
				abmOrgs: []string{"Acme Inc"},
			},
			wantSaves: 1,
			wantABM: []automdm.MDMAppleABMAssignmentInfo{
				{OrganizationName: "Acme Inc", MacOSTeam: "", IOSTeam: "Phones"},
			},
		},
		{
			name: "other ABM tokens are left alone",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{
					{OrganizationName: "Acme Inc", MacOSTeam: "Workstations"},
					{OrganizationName: "Beta Corp", MacOSTeam: "Workstations"},
				},
				abmOrgs: []string{"Acme Inc"},
			},
			wantSaves: 1,
			wantABM: []automdm.MDMAppleABMAssignmentInfo{
				{OrganizationName: "Acme Inc", MacOSTeam: ""},
				{OrganizationName: "Beta Corp", MacOSTeam: "Workstations"},
			},
		},
		{
			name: "team isn't referenced",
			fixture: staleTeamNamesFixture{
				abmStored: []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Servers"}},
			},
			wantABM: []automdm.MDMAppleABMAssignmentInfo{{OrganizationName: "Acme Inc", MacOSTeam: "Servers"}},
		},
	}

	ctx := test.UserContext(context.Background(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc, ds, appCfg, saves := newStaleTeamNamesService(t, teamID, tc.fixture)
			ds.TeamLiteFunc = func(_ context.Context, tid uint) (*automdm.TeamLite, error) {
				return &automdm.TeamLite{ID: tid, Name: "Workstations"}, nil
			}
			ds.ListHostsFunc = func(context.Context, automdm.TeamFilter, automdm.HostListOptions) ([]*automdm.Host, error) {
				return nil, nil
			}
			ds.GetCertificateTemplatesByTeamIDFunc = func(context.Context, uint, automdm.ListOptions) (
				[]*automdm.CertificateTemplateResponseSummary, *automdm.PaginationMetadata, error,
			) {
				return nil, nil, nil
			}
			ds.GetWindowsEnrollmentDefaultTeamFunc = func(context.Context) (*uint, string, error) { return nil, "", nil }
			ds.DeleteTeamFunc = func(context.Context, uint) error { return nil }

			require.NoError(t, svc.DeleteTeam(ctx, teamID))

			require.True(t, ds.GetABMTokenOrgNamesAssociatedByDefaultTeamsFuncInvoked)
			require.Equal(t, tc.wantSaves, *saves)
			require.Equal(t, tc.wantABM, appCfg.MDM.AppleBusinessManager.Value)
		})
	}
}

func TestModifyTeamPartialSetupExperienceKeepsEndUserAuth(t *testing.T) {
	authorizer, err := authz.NewAuthorizer()
	require.NoError(t, err)
	ctx := test.UserContext(t.Context(), &automdm.User{ID: 1, GlobalRole: new(automdm.RoleAdmin)})

	ds := new(mock.Store)
	ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) {
		return &automdm.AppConfig{}, nil
	}
	ds.TeamWithExtrasFunc = func(_ context.Context, tid uint) (*automdm.Team, error) {
		tm := &automdm.Team{ID: tid, Name: "team-1"}
		tm.Config.MDM.MacOSSetup.EnableEndUserAuthentication = true
		tm.Config.MDM.MacOSSetup.LockEndUserInfo = optjson.SetBool(true)
		return tm, nil
	}
	var saved *automdm.Team
	ds.SaveTeamFunc = func(_ context.Context, team *automdm.Team) (*automdm.Team, error) {
		saved = team
		return team, nil
	}

	mockSvc := &svcmock.Service{}
	mockSvc.HasCustomSetupAssistantConfigurationWebURLFunc = func(context.Context, *uint) (bool, error) {
		return false, nil
	}
	mockSvc.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error { return nil }

	svc := &Service{
		Service: mockSvc,
		ds:      ds,
		config:  config.AutoMDMConfig{Server: config.ServerConfig{PrivateKey: "something"}},
		authz:   authorizer,
		logger:  slog.New(slog.DiscardHandler),
	}

	// Decode from JSON so the payload carries only the keys a client sent.
	var payload automdm.TeamPayload
	require.NoError(t, json.Unmarshal([]byte(`{"mdm": {"setup_experience": {"lock_end_user_info": false}}}`), &payload))

	_, err = svc.ModifyTeam(ctx, 1, payload)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.True(t, saved.Config.MDM.MacOSSetup.EnableEndUserAuthentication)
	require.False(t, saved.Config.MDM.MacOSSetup.LockEndUserInfo.Value)
}
