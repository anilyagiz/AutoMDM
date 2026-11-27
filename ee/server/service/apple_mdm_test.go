package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/authz"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	nanodep_client "github.com/anilyagiz/automdm/v4/server/mdm/nanodep/client"
	"github.com/anilyagiz/automdm/v4/server/mock"
	nanodep_mock "github.com/anilyagiz/automdm/v4/server/mock/nanodep"
	svcmock "github.com/anilyagiz/automdm/v4/server/mock/service"
	common_mysql "github.com/anilyagiz/automdm/v4/server/platform/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAppleDDMAssets(t *testing.T) {
	ds := new(mock.Store)
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("Observer cannot list DDM assets", func(t *testing.T) {
		ds.ListAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.DDMAsset, error) {
			return []*automdm.DDMAsset{}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})

		_, err := svc.ListAppleDDMAssets(ctx, nil)
		require.Error(t, err)
		var forbiddenErr *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenErr)
		require.False(t, ds.ListAppleDDMAssetsFuncInvoked)

		ds.ListAppleDDMAssetsFuncInvoked = false
	})

	t.Run("Global admin can list DDM assets", func(t *testing.T) {
		ds.ListAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.DDMAsset, error) {
			return []*automdm.DDMAsset{}, nil
		}

		_, err := svc.ListAppleDDMAssets(ctx, nil)
		require.NoError(t, err)
		require.True(t, ds.ListAppleDDMAssetsFuncInvoked)

		ds.ListAppleDDMAssetsFuncInvoked = false
	})

	t.Run("Team admin can list DDM assets for their team", func(t *testing.T) {
		ds.ListAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.DDMAsset, error) {
			return []*automdm.DDMAsset{}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin), Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})

		_, err := svc.ListAppleDDMAssets(ctx, new(uint(1)))
		require.NoError(t, err)
		require.True(t, ds.ListAppleDDMAssetsFuncInvoked)

		ds.ListAppleDDMAssetsFuncInvoked = false
	})

	t.Run("Team admin cannot list DDM assets for other teams", func(t *testing.T) {
		ds.ListAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint) ([]*automdm.DDMAsset, error) {
			return []*automdm.DDMAsset{}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})

		_, err := svc.ListAppleDDMAssets(ctx, new(uint(2)))
		require.Error(t, err)
		var forbiddenErr *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenErr)
		require.False(t, ds.ListAppleDDMAssetsFuncInvoked)

		ds.ListAppleDDMAssetsFuncInvoked = false
	})
}

func TestGetAppleDDMAsset(t *testing.T) {
	ds := new(mock.Store)
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("Observer cannot get DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})
		_, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team Observer cannot get DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}})
		_, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Global admin can get DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		asset, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.Equal(t, "some-asset-uuid", asset.AssetUUID)
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin can get DDM asset for their team", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID, TeamID: new(uint(1))}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		asset, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.Equal(t, "some-asset-uuid", asset.AssetUUID)
		require.Equal(t, uint(1), *asset.TeamID)
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin cannot get DDM asset for other teams", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID, TeamID: new(uint(2))}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		_, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))

		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Not found asset returns not found error", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return nil, common_mysql.NotFound("asset")
		}

		_, err := svc.GetAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})
}

func TestDownloadAppleDDMAsset(t *testing.T) {
	ds := new(mock.Store)
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("Observer cannot download DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return &automdm.DownloadableDDMAsset{DDMAsset: automdm.DDMAsset{AssetUUID: assetUUID}, Data: []byte("some data")}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})
		_, _, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})

	t.Run("Team Observer cannot download DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return &automdm.DownloadableDDMAsset{DDMAsset: automdm.DDMAsset{AssetUUID: assetUUID}, Data: []byte("some data")}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}})
		_, _, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})

	t.Run("Global admin can download DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return &automdm.DownloadableDDMAsset{DDMAsset: automdm.DDMAsset{AssetUUID: assetUUID, Name: assetUUID}, Data: []byte("some data")}, nil
		}
		name, data, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.Equal(t, "some-asset-uuid.json", name)
		require.Equal(t, []byte("some data"), data)
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})

	t.Run("Team admin can download DDM asset for their team", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return &automdm.DownloadableDDMAsset{DDMAsset: automdm.DDMAsset{AssetUUID: assetUUID, Name: assetUUID, TeamID: new(uint(1))}, Data: []byte("some data")}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		name, data, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.Equal(t, "some-asset-uuid.json", name)
		require.Equal(t, []byte("some data"), data)
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})

	t.Run("Team admin cannot download DDM asset for other teams", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return &automdm.DownloadableDDMAsset{DDMAsset: automdm.DDMAsset{AssetUUID: assetUUID, TeamID: new(uint(2))}, Data: []byte("some data")}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		_, _, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})

	t.Run("Not found asset returns not found error", func(t *testing.T) {
		ds.GetAppleDDMAssetForDownloadFunc = func(ctx context.Context, assetUUID string) (*automdm.DownloadableDDMAsset, error) {
			return nil, common_mysql.NotFound("asset")
		}

		_, _, err := svc.DownloadAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetForDownloadFuncInvoked)

		ds.GetAppleDDMAssetForDownloadFuncInvoked = false
	})
}

func TestDeleteAppleDDMAsset(t *testing.T) {
	ds := new(mock.Store)
	svc, mockSvc := newTestServiceWithMock(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	t.Run("Observer cannot delete DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})
		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team Observer cannot delete DDM asset", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleObserver}}}})
		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
	})

	t.Run("Global admin can delete DDM asset", func(t *testing.T) {
		mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID}, nil
		}
		ds.DeleteAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) error {
			return nil
		}

		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)
		require.True(t, ds.DeleteAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
		ds.DeleteAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin can delete DDM asset for their team", func(t *testing.T) {
		mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID, TeamID: new(uint(1))}, nil
		}
		ds.DeleteAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) error {
			return nil
		}
		ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
			return &automdm.TeamLite{ID: tid, Name: "team"}, nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})

		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.NoError(t, err)
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)
		require.True(t, ds.DeleteAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
		ds.DeleteAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin cannot delete DDM asset for other teams", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return &automdm.DDMAsset{AssetUUID: assetUUID, TeamID: new(uint(2))}, nil
		}
		ds.DeleteAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) error {
			return nil
		}
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})

		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)
		require.False(t, ds.DeleteAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
		ds.DeleteAppleDDMAssetFuncInvoked = false
	})

	t.Run("Not found asset returns not found error", func(t *testing.T) {
		ds.GetAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) (*automdm.DDMAsset, error) {
			return nil, common_mysql.NotFound("asset")
		}
		ds.DeleteAppleDDMAssetFunc = func(ctx context.Context, assetUUID string) error {
			return nil
		}

		err := svc.DeleteAppleDDMAsset(ctx, "some-asset-uuid")
		require.Error(t, err)
		require.True(t, automdm.IsNotFound(err))
		require.True(t, ds.GetAppleDDMAssetFuncInvoked)
		require.False(t, ds.DeleteAppleDDMAssetFuncInvoked)

		ds.GetAppleDDMAssetFuncInvoked = false
		ds.DeleteAppleDDMAssetFuncInvoked = false
	})
}

func TestCreateAppleDDMAsset(t *testing.T) {
	ds := new(mock.Store)
	svc, mockSvc := newTestServiceWithMock(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	validData := []byte(`{"Type":"com.apple.asset.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"https://example.com/data"}}}`)

	ds.CreateAppleDDMAssetFunc = func(ctx context.Context, name, identifier string, data []byte, teamID *uint) (string, error) {
		return "some-asset-uuid", nil
	}
	ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
		return document, nil, nil
	}

	reset := func() {
		ds.CreateAppleDDMAssetFuncInvoked = false
		ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked = false
	}

	t.Run("Observer cannot create DDM asset", func(t *testing.T) {
		defer reset()
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", validData)
		require.Error(t, err)
		var forbiddenErr *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenErr)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)

		ds.CreateAppleDDMAssetFuncInvoked = false
	})

	t.Run("Global admin can create DDM asset", func(t *testing.T) {
		mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}
		defer reset()
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", validData)
		require.NoError(t, err)
		require.True(t, ds.CreateAppleDDMAssetFuncInvoked)

		ds.CreateAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin can create DDM asset for their team", func(t *testing.T) {
		mockSvc.NewActivityFunc = func(ctx context.Context, user *automdm.User, activity automdm.ActivityDetails) error {
			return nil
		}
		ds.TeamLiteFunc = func(ctx context.Context, tid uint) (*automdm.TeamLite, error) {
			return &automdm.TeamLite{ID: tid, Name: "team"}, nil
		}
		defer reset()
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		_, err := svc.CreateAppleDDMAsset(ctx, new(uint(1)), "asset", validData)
		require.NoError(t, err)
		require.True(t, ds.CreateAppleDDMAssetFuncInvoked)

		ds.CreateAppleDDMAssetFuncInvoked = false
	})

	t.Run("Team admin cannot create DDM asset for other teams", func(t *testing.T) {
		defer reset()
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: nil, Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1}, Role: automdm.RoleAdmin}}}})
		_, err := svc.CreateAppleDDMAsset(ctx, new(uint(2)), "asset", validData)
		require.Error(t, err)
		var forbiddenErr *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenErr)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)

		ds.CreateAppleDDMAssetFuncInvoked = false
	})

	t.Run("Malformed JSON is rejected", func(t *testing.T) {
		defer reset()
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", []byte(`{not json`))
		require.Error(t, err)
		require.False(t, ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Empty identifier is rejected", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"com.apple.asset.data","Identifier":"","Payload":{"Reference":{"DataURL":"https://example.com/data"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Invalid asset type is rejected", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"com.example.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"https://example.com/data"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Empty payload reference data URL is rejected", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"com.apple.asset.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":""}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Invalid payload reference data URL is rejected", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"com.apple.asset.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"notaurl"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Secret in type is rejected before expansion", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"$AUTOMDM_SECRET_TYPE","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"https://example.com/data"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Secret in identifier is rejected before expansion", func(t *testing.T) {
		defer reset()
		data := []byte(`{"Type":"com.apple.asset.data","Identifier":"$AUTOMDM_SECRET_ID","Payload":{"Reference":{"DataURL":"https://example.com/data"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.Error(t, err)
		require.False(t, ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Secret in payload data URL is expanded and allowed", func(t *testing.T) {
		defer reset()
		ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
			return string(validData), nil, nil
		}
		defer func() {
			ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
				return document, nil, nil
			}
		}()
		data := []byte(`{"Type":"com.apple.asset.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"$AUTOMDM_SECRET_URL"}}}`)
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", data)
		require.NoError(t, err)
		require.True(t, ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked)
		require.True(t, ds.CreateAppleDDMAssetFuncInvoked)
	})

	t.Run("Expanded payload with authentication key is rejected", func(t *testing.T) {
		defer reset()
		ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
			return `{"Type":"com.apple.asset.data","Identifier":"com.example.asset","Payload":{"Reference":{"DataURL":"https://example.com/data"},"Authentication":{"Username":"u"}}}`, nil, nil
		}
		defer func() {
			ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
				return document, nil, nil
			}
		}()
		_, err := svc.CreateAppleDDMAsset(ctx, nil, "asset", validData)
		require.Error(t, err)
		require.True(t, ds.ExpandEmbeddedSecretsAndUpdatedAtFuncInvoked)
		require.False(t, ds.CreateAppleDDMAssetFuncInvoked)
	})
}

func TestBatchSetAppleDDMAssets(t *testing.T) {
	ds := new(mock.Store)
	svc := newTestService(t, ds)
	ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})

	assetData := func(identifier string) []byte {
		return []byte(fmt.Sprintf(`{"Type":"com.apple.asset.data","Identifier":%q,"Payload":{"Reference":{"DataURL":"https://example.com/%s"}}}`, identifier, identifier))
	}

	ds.ExpandEmbeddedSecretsAndUpdatedAtFunc = func(ctx context.Context, document string) (string, *time.Time, error) {
		return document, nil, nil
	}
	ds.BatchSetAppleDDMAssetsFunc = func(ctx context.Context, teamID *uint, assets []*automdm.MDMAppleDDMAssetToSet) (*automdm.MDMAppleDDMAssetsBatchChanges, error) {
		return &automdm.MDMAppleDDMAssetsBatchChanges{}, nil
	}
	reset := func() { ds.BatchSetAppleDDMAssetsFuncInvoked = false }

	t.Run("Observer cannot batch set", func(t *testing.T) {
		defer reset()
		ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleObserver)}})
		err := svc.BatchSetAppleDDMAssets(ctx, nil, "", []automdm.MDMAppleDDMAssetBatchPayload{{Name: "a", Contents: assetData("id.a")}}, false)
		require.Error(t, err)
		var forbiddenErr *authz.Forbidden
		require.ErrorAs(t, err, &forbiddenErr)
		require.False(t, ds.BatchSetAppleDDMAssetsFuncInvoked)
	})

	t.Run("Global admin can batch set", func(t *testing.T) {
		defer reset()
		err := svc.BatchSetAppleDDMAssets(ctx, nil, "", []automdm.MDMAppleDDMAssetBatchPayload{
			{Name: "a", Contents: assetData("id.a")},
			{Name: "b", Contents: assetData("id.b")},
		}, false)
		require.NoError(t, err)
		require.True(t, ds.BatchSetAppleDDMAssetsFuncInvoked)
	})

	t.Run("Duplicate identifier is rejected", func(t *testing.T) {
		defer reset()
		err := svc.BatchSetAppleDDMAssets(ctx, nil, "", []automdm.MDMAppleDDMAssetBatchPayload{
			{Name: "a", Contents: assetData("id.dup")},
			{Name: "b", Contents: assetData("id.dup")},
		}, false)
		require.Error(t, err)
		require.False(t, ds.BatchSetAppleDDMAssetsFuncInvoked)
	})

	t.Run("Duplicate name is rejected", func(t *testing.T) {
		defer reset()
		err := svc.BatchSetAppleDDMAssets(ctx, nil, "", []automdm.MDMAppleDDMAssetBatchPayload{
			{Name: "same", Contents: assetData("id.a")},
			{Name: "same", Contents: assetData("id.b")},
		}, false)
		require.Error(t, err)
		require.False(t, ds.BatchSetAppleDDMAssetsFuncInvoked)
	})

	t.Run("Dry run does not write", func(t *testing.T) {
		defer reset()
		err := svc.BatchSetAppleDDMAssets(ctx, nil, "", []automdm.MDMAppleDDMAssetBatchPayload{
			{Name: "a", Contents: assetData("id.a")},
		}, true)
		require.NoError(t, err)
		require.False(t, ds.BatchSetAppleDDMAssetsFuncInvoked)
	})
}

func appleHost(id uint, serial string, teamID *uint) *automdm.Host {
	return &automdm.Host{ID: id, Platform: "darwin", HardwareSerial: serial, TeamID: teamID, Hostname: fmt.Sprintf("host-%d", id)}
}

func depAssignment(hostID uint, serial string, tokenID *uint) *automdm.HostDEPAssignment {
	return &automdm.HostDEPAssignment{HostID: hostID, HardwareSerial: serial, ABMTokenID: tokenID}
}

// startDEPServer stands in for Apple's DEP API. sessionStatus/disownStatus of 0
// mean 200. serialStatus overrides the per-serial status echoed by /devices/disown
// (defaults to SUCCESS).
func startDEPServer(t *testing.T, sessionStatus, disownStatus int, serialStatus map[string]string) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/session"):
			if sessionStatus != 0 && sessionStatus != http.StatusOK {
				w.WriteHeader(sessionStatus)
				_, _ = w.Write([]byte(`{"error":"FORBIDDEN"}`))
				return
			}
			_, _ = w.Write([]byte(`{"auth_session_token":"tok"}`))
		case strings.Contains(r.URL.Path, "/devices/disown"):
			if disownStatus != 0 && disownStatus != http.StatusOK {
				w.WriteHeader(disownStatus)
				return
			}
			var req struct {
				Devices []string `json:"devices"`
			}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			out := make(map[string]string, len(req.Devices))
			for _, s := range req.Devices {
				st := "SUCCESS"
				if v, ok := serialStatus[s]; ok {
					st = v
				}
				out[s] = st
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"devices": out}))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func setupReleaseABTest(t *testing.T) (*Service, *mock.Store, *nanodep_mock.Storage, *svcmock.Service) {
	ds := new(mock.Store)
	svc, base := newTestServiceWithMock(t, ds)
	svc.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	base.NewActivityFunc = func(context.Context, *automdm.User, automdm.ActivityDetails) error { return nil }

	// Short-circuit the DEP client's terms-expired and token-invalid after-hooks.
	ds.CountABMTokensWithTermsExpiredFunc = func(context.Context) (int, error) { return 0, nil }
	ds.AppConfigFunc = func(context.Context) (*automdm.AppConfig, error) { return &automdm.AppConfig{}, nil }
	ds.SetABMTokenInvalidForOrgNameFunc = func(context.Context, string, bool) (bool, error) { return false, nil }
	ds.IsABMTokenInvalidForOrgNameFunc = func(context.Context, string) (bool, error) { return false, nil }
	ds.DeleteHostDEPAssignmentsFunc = func(context.Context, uint, []string) error { return nil }

	dep := &nanodep_mock.Storage{}
	dep.RetrieveAuthTokensFunc = func(context.Context, string) (*nanodep_client.OAuth1Tokens, error) {
		return &nanodep_client.OAuth1Tokens{ConsumerKey: "ck", ConsumerSecret: "cs", AccessToken: "at", AccessSecret: "as"}, nil
	}
	svc.depStorage = dep
	return svc, ds, dep, base
}

func byHostID(resp []*automdm.ABReleaseDeviceResponse) map[uint]*automdm.ABReleaseDeviceResponse {
	m := make(map[uint]*automdm.ABReleaseDeviceResponse, len(resp))
	for _, r := range resp {
		m[r.HostID] = r
	}
	return m
}

func adminCtx() context.Context {
	return viewer.NewContext(context.Background(), viewer.Viewer{User: &automdm.User{GlobalRole: new(automdm.RoleAdmin)}})
}

func TestReleaseABDevicesAuthorization(t *testing.T) {
	team1 := uint(1)
	team2 := uint(2)

	setup := func(t *testing.T) (*Service, *mock.Store) {
		svc, ds, _, _ := setupReleaseABTest(t)
		ds.ListHostsLiteByIDsFunc = func(_ context.Context, ids []uint) ([]*automdm.Host, error) {
			return []*automdm.Host{
				appleHost(1, "S1", &team1),
				appleHost(2, "S2", &team2),
			}, nil
		}
		// No DEP assignments so authorized calls short-circuit before hitting Apple.
		ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
			return nil, nil
		}
		ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
			return nil, nil
		}
		return svc, ds
	}

	cases := []struct {
		name    string
		user    *automdm.User
		wantErr bool
	}{
		{"global admin", &automdm.User{GlobalRole: new(automdm.RoleAdmin)}, false},
		{"global observer", &automdm.User{GlobalRole: new(automdm.RoleObserver)}, true},
		{"global maintainer", &automdm.User{GlobalRole: new(automdm.RoleMaintainer)}, true},
		{"global gitops", &automdm.User{GlobalRole: new(automdm.RoleGitOps)}, true},
		{"team admin missing one team", &automdm.User{Teams: []automdm.UserTeam{{Team: automdm.Team{ID: team1}, Role: automdm.RoleAdmin}}}, true},
		{"team admin all teams", &automdm.User{Teams: []automdm.UserTeam{
			{Team: automdm.Team{ID: team1}, Role: automdm.RoleAdmin},
			{Team: automdm.Team{ID: team2}, Role: automdm.RoleAdmin},
		}}, false},
		{"team admin wrong role", &automdm.User{Teams: []automdm.UserTeam{
			{Team: automdm.Team{ID: team1}, Role: automdm.RoleObserver},
			{Team: automdm.Team{ID: team2}, Role: automdm.RoleObserver},
		}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := setup(t)
			ctx := viewer.NewContext(context.Background(), viewer.Viewer{User: c.user})
			_, err := svc.ReleaseABDevices(ctx, []uint{1, 2})
			if c.wantErr {
				var forbidden *authz.Forbidden
				require.ErrorAs(t, err, &forbidden)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestReleaseABDevicesTooManyHosts(t *testing.T) {
	svc, ds, _, _ := setupReleaseABTest(t)
	ids := make([]uint, 32_001)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	_, err := svc.ReleaseABDevices(adminCtx(), ids)
	var badReq *automdm.BadRequestError
	require.ErrorAs(t, err, &badReq)
	require.False(t, ds.ListHostsLiteByIDsFuncInvoked)
}

func TestReleaseABDevicesDatastoreError(t *testing.T) {
	svc, ds, _, _ := setupReleaseABTest(t)
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return nil, io.ErrUnexpectedEOF
	}
	_, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.Error(t, err)
}

func TestReleaseABDevicesPerHostErrors(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	tokenID := uint(10)
	otherToken := uint(99)

	// host 1: apple + assigned -> success
	// host 2: not returned by lite lookup -> not found
	// host 3: non-apple platform -> ineligible
	// host 4: apple but no DEP assignment -> not in AB
	// host 5: apple, assignment with nil token -> no ABM token
	// host 6: apple, assignment references unknown token -> token not found
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		h3 := appleHost(3, "S3", nil)
		h3.Platform = "windows"
		return []*automdm.Host{
			appleHost(1, "S1", nil),
			h3,
			appleHost(4, "S4", nil),
			appleHost(5, "S5", nil),
			appleHost(6, "S6", nil),
		}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{
			depAssignment(1, "S1", &tokenID),
			depAssignment(5, "S5", nil),
			depAssignment(6, "S6", &otherToken),
		}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	ts := startDEPServer(t, 0, 0, nil)
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1, 2, 3, 4, 5, 6})
	require.NoError(t, err)
	m := byHostID(resp)
	require.NotNil(t, m[1])
	require.NotNil(t, m[2])
	require.NotNil(t, m[3])
	require.NotNil(t, m[4])
	require.NotNil(t, m[5])
	require.NotNil(t, m[6])

	require.Equal(t, string(automdm.ABReleaseDeviceStatusSuccess), m[1].Status)
	require.Empty(t, m[1].Error)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusError), m[2].Status)
	require.Contains(t, m[2].Error, "Host not found")
	require.Contains(t, m[3].Error, "not an eligible Apple host")
	require.Contains(t, m[4].Error, "not found in Apple Business")
	require.Contains(t, m[5].Error, "no associated ABM token")
	require.Contains(t, m[6].Error, "ABM token not found")

	// Responses are sorted by host ID.
	for i := 1; i < len(resp); i++ {
		require.Less(t, resp[i-1].HostID, resp[i].HostID)
	}
}

func TestReleaseABDevicesDEPAuthError(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	tokenID := uint(10)
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{appleHost(1, "S1", nil)}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{depAssignment(1, "S1", &tokenID)}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	ts := startDEPServer(t, http.StatusForbidden, 0, nil)
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusError), resp[0].Status)
	require.Contains(t, resp[0].Error, "Apple rejected this request")
}

func TestReleaseABDevicesDEPGenericError(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	tokenID := uint(10)
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{appleHost(1, "S1", nil)}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{depAssignment(1, "S1", &tokenID)}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	ts := startDEPServer(t, 0, http.StatusInternalServerError, nil)
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusError), resp[0].Status)
	require.Contains(t, resp[0].Error, "Couldn't release host from Apple Business.") // full error is logged on the server side
	require.NotContains(t, resp[0].Error, "Apple rejected this request")
}

func TestReleaseABDevicesNonSuccessStatus(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	tokenID := uint(10)
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{appleHost(1, "S1", nil), appleHost(2, "S2", nil)}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{depAssignment(1, "S1", &tokenID), depAssignment(2, "S2", &tokenID)}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	ts := startDEPServer(t, 0, 0, map[string]string{"S2": "NOT_ACCESSIBLE"})
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1, 2})
	require.NoError(t, err)
	m := byHostID(resp)
	require.NotNil(t, m[1])
	require.NotNil(t, m[2])

	require.Equal(t, string(automdm.ABReleaseDeviceStatusSuccess), m[1].Status)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusError), m[2].Status)
	require.Contains(t, m[2].Error, "NOT_ACCESSIBLE")
}

func TestReleaseABDevicesActivityLogged(t *testing.T) {
	svc, ds, dep, base := setupReleaseABTest(t)
	tokenID := uint(10)
	var logged []automdm.ActivityTypeReleasedDeviceFromAB
	base.NewActivityFunc = func(_ context.Context, _ *automdm.User, act automdm.ActivityDetails) error {
		a, ok := act.(automdm.ActivityTypeReleasedDeviceFromAB)
		require.True(t, ok)
		logged = append(logged, a)
		return nil
	}
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{appleHost(1, "S1", nil)}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{depAssignment(1, "S1", &tokenID)}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	var deletedToken uint
	var deletedSerials []string
	ds.DeleteHostDEPAssignmentsFunc = func(_ context.Context, abmTokenID uint, serials []string) error {
		deletedToken = abmTokenID
		deletedSerials = serials
		return nil
	}
	ts := startDEPServer(t, 0, 0, nil)
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	_, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.NoError(t, err)
	require.True(t, base.NewActivityFuncInvoked)
	require.Len(t, logged, 1)
	require.Equal(t, uint(1), logged[0].HostID)
	require.Equal(t, "S1", logged[0].HostSerial)

	// Released devices have their DEP assignment cleared under the right token.
	require.True(t, ds.DeleteHostDEPAssignmentsFuncInvoked)
	require.Equal(t, tokenID, deletedToken)
	require.Equal(t, []string{"S1"}, deletedSerials)
}

// TestReleaseABDevicesMultiToken exercises devices spread across two ABM tokens,
// where one token's disown call is rejected by Apple and the other succeeds.
func TestReleaseABDevicesMultiToken(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	token1 := uint(10)
	token2 := uint(20)

	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{
			appleHost(1, "S1", nil),
			appleHost(2, "S2", nil),
			appleHost(3, "S3", nil),
		}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{
			depAssignment(1, "S1", &token1),
			depAssignment(2, "S2", &token1),
			depAssignment(3, "S3", &token2),
		}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{
			{ID: token1, OrganizationName: "Org1"},
			{ID: token2, OrganizationName: "Org2"},
		}, nil
	}

	deleted := map[uint][]string{}
	ds.DeleteHostDEPAssignmentsFunc = func(_ context.Context, abmTokenID uint, serials []string) error {
		deleted[abmTokenID] = serials
		return nil
	}

	// Org1 succeeds, Org2's session is rejected (auth error).
	okServer := startDEPServer(t, 0, 0, nil)
	authFailServer := startDEPServer(t, http.StatusForbidden, 0, nil)
	seen := map[string]struct{}{}
	dep.RetrieveConfigFunc = func(_ context.Context, name string) (*nanodep_client.Config, error) {
		seen[name] = struct{}{}
		if name == "Org2" {
			return &nanodep_client.Config{BaseURL: authFailServer.URL}, nil
		}
		return &nanodep_client.Config{BaseURL: okServer.URL}, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1, 2, 3})
	require.NoError(t, err)
	m := byHostID(resp)
	require.NotNil(t, m[1])
	require.NotNil(t, m[2])
	require.NotNil(t, m[3])

	require.Equal(t, string(automdm.ABReleaseDeviceStatusSuccess), m[1].Status)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusSuccess), m[2].Status)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusError), m[3].Status)
	require.Contains(t, m[3].Error, "Apple rejected this request")

	// Each token was resolved by its own organization name.
	_, ok := seen["Org1"]
	assert.True(t, ok)
	_, ok = seen["Org2"]
	assert.True(t, ok)

	// Only the successful token clears its assignments; the failed token does not.
	require.ElementsMatch(t, []string{"S1", "S2"}, deleted[token1])
	require.NotContains(t, deleted, token2)
}

func TestReleaseABDevicesAllIneligibleSkipsDEP(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	h := appleHost(1, "S1", nil)
	h.Platform = "ubuntu"
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{h}, nil
	}
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		t.Fatal("DEP should not be contacted when there are no eligible hosts")
		return nil, nil
	}

	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Contains(t, resp[0].Error, "not an eligible Apple host")
	require.False(t, ds.GetHostDEPAssignmentsByHostIDsFuncInvoked)
}

func TestReleaseABDevicesDeleteAssignmentErrorIsNonFatal(t *testing.T) {
	svc, ds, dep, _ := setupReleaseABTest(t)
	tokenID := uint(10)
	ds.ListHostsLiteByIDsFunc = func(context.Context, []uint) ([]*automdm.Host, error) {
		return []*automdm.Host{appleHost(1, "S1", nil)}, nil
	}
	ds.GetHostDEPAssignmentsByHostIDsFunc = func(context.Context, []uint) ([]*automdm.HostDEPAssignment, error) {
		return []*automdm.HostDEPAssignment{depAssignment(1, "S1", &tokenID)}, nil
	}
	ds.ListABMTokensFunc = func(context.Context) ([]*automdm.ABMToken, error) {
		return []*automdm.ABMToken{{ID: tokenID, OrganizationName: "Org1"}}, nil
	}
	ds.DeleteHostDEPAssignmentsFunc = func(context.Context, uint, []string) error {
		return io.ErrUnexpectedEOF
	}
	ts := startDEPServer(t, 0, 0, nil)
	dep.RetrieveConfigFunc = func(context.Context, string) (*nanodep_client.Config, error) {
		return &nanodep_client.Config{BaseURL: ts.URL}, nil
	}

	// The device was released, so the call still succeeds even though clearing
	// the DEP assignment failed (that error is only logged).
	resp, err := svc.ReleaseABDevices(adminCtx(), []uint{1})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, string(automdm.ABReleaseDeviceStatusSuccess), resp[0].Status)
	require.True(t, ds.DeleteHostDEPAssignmentsFuncInvoked)
}

func TestHandleSelfServiceConfigurationProfile(t *testing.T) {
	ctx := context.Background()
	macHost := &automdm.Host{ID: 1, UUID: "host-uuid", Platform: "darwin", LabelUpdatedAt: time.Now()}
	selfService := func() *automdm.AppleProfileForReconcile {
		return &automdm.AppleProfileForReconcile{ProfileUUID: "prof-uuid", ProfileName: "SS", SelfService: true}
	}

	cases := []struct {
		name      string
		uninstall bool
		host      *automdm.Host
		optedIn   bool
		mdmOff    bool
		hostRow   *automdm.HostMDMAppleProfile
		profile   *automdm.AppleProfileForReconcile
		members   []uint
		queueErr  error
		wantErr   error
	}{
		{name: "install non-macOS", host: &automdm.Host{UUID: "h", Platform: "windows"}, wantErr: &automdm.BadRequestError{}},
		{name: "install MDM off", host: macHost, mdmOff: true, profile: selfService(), wantErr: &automdm.BadRequestError{}},
		{
			name: "install while removal pending", host: macHost, profile: selfService(),
			hostRow: &automdm.HostMDMAppleProfile{ProfileUUID: "prof-uuid", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			wantErr: &automdm.ConflictError{},
		},
		{
			name: "uninstall while install pending", uninstall: true, host: macHost, optedIn: true, profile: selfService(),
			hostRow: &automdm.HostMDMAppleProfile{ProfileUUID: "prof-uuid", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryPending},
			wantErr: &automdm.ConflictError{},
		},
		{
			name: "uninstall while removal pending", uninstall: true, host: macHost, optedIn: true, profile: selfService(),
			hostRow: &automdm.HostMDMAppleProfile{ProfileUUID: "prof-uuid", OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending},
			wantErr: &automdm.ConflictError{},
		},
		{
			name: "uninstall after install verified", uninstall: true, host: macHost, optedIn: true, profile: selfService(),
			hostRow: &automdm.HostMDMAppleProfile{ProfileUUID: "prof-uuid", OperationType: automdm.MDMOperationTypeInstall, Status: &automdm.MDMDeliveryVerified},
		},
		{name: "install already opted in", host: macHost, optedIn: true, profile: selfService(), wantErr: &automdm.ConflictError{}},
		{name: "install profile not found", host: macHost, wantErr: &automdm.BadRequestError{}},
		{name: "install not self-service", host: macHost, profile: &automdm.AppleProfileForReconcile{ProfileUUID: "prof-uuid"}, wantErr: &automdm.BadRequestError{}},
		{
			name: "install label not satisfied", host: macHost,
			profile: func() *automdm.AppleProfileForReconcile {
				p := selfService()
				p.IncludeMode = automdm.AppleProfileIncludeAll
				p.IncludeLabels = []automdm.AppleProfileLabelRef{{LabelID: new(uint(10)), LabelMembershipType: int(automdm.LabelMembershipTypeManual)}}
				return p
			}(),
			wantErr: &automdm.BadRequestError{},
		},
		{
			name: "install broken label", host: macHost,
			profile: func() *automdm.AppleProfileForReconcile {
				p := selfService()
				p.IncludeMode = automdm.AppleProfileIncludeAll
				p.IncludeLabels = []automdm.AppleProfileLabelRef{{LabelID: nil}}
				return p
			}(),
			wantErr: &automdm.BadRequestError{},
		},
		{
			name: "install label satisfied", host: macHost, members: []uint{10},
			profile: func() *automdm.AppleProfileForReconcile {
				p := selfService()
				p.IncludeMode = automdm.AppleProfileIncludeAll
				p.IncludeLabels = []automdm.AppleProfileLabelRef{{LabelID: new(uint(10)), LabelMembershipType: int(automdm.LabelMembershipTypeManual)}}
				return p
			}(),
		},
		{
			name: "install unknown dynamic label membership", host: macHost,
			profile: func() *automdm.AppleProfileForReconcile {
				p := selfService()
				p.ExcludeLabels = []automdm.AppleProfileLabelRef{{LabelID: new(uint(11)), CreatedAt: macHost.LabelUpdatedAt.Add(time.Hour)}}
				return p
			}(),
			wantErr: &automdm.BadRequestError{},
		},
		{name: "install", host: macHost, profile: selfService()},
		{name: "install queue failure is not fatal", host: macHost, profile: selfService(), queueErr: errors.New("boom")},
		{name: "uninstall non-macOS", uninstall: true, host: &automdm.Host{UUID: "h", Platform: "ios"}, wantErr: &automdm.BadRequestError{}},
		{name: "uninstall not opted in", uninstall: true, host: macHost, profile: selfService(), wantErr: &notFoundError{}},
		{name: "uninstall profile not found", uninstall: true, host: macHost, optedIn: true, wantErr: &automdm.BadRequestError{}},
		{name: "uninstall not self-service", uninstall: true, host: macHost, optedIn: true, profile: &automdm.AppleProfileForReconcile{ProfileUUID: "prof-uuid"}, wantErr: &automdm.BadRequestError{}},
		{
			name: "uninstall broken label", uninstall: true, host: macHost, optedIn: true,
			profile: func() *automdm.AppleProfileForReconcile {
				p := selfService()
				p.ExcludeLabels = []automdm.AppleProfileLabelRef{{LabelID: nil}}
				return p
			}(),
			wantErr: &automdm.BadRequestError{},
		},
		{name: "uninstall", uninstall: true, host: macHost, optedIn: true, profile: selfService()},
		{name: "uninstall queue failure is not fatal", uninstall: true, host: macHost, optedIn: true, profile: selfService(), queueErr: errors.New("boom")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := new(mock.Store)
			svc := newTestService(t, ds)
			ds.IsHostConnectedToAutoMDMMDMFunc = func(ctx context.Context, host *automdm.Host) (bool, error) {
				return !c.mdmOff, nil
			}
			ds.HasHostMDMProfileOptInFunc = func(ctx context.Context, hostUUID, profileUUID string) (bool, error) {
				return c.optedIn, nil
			}
			ds.GetHostMDMAppleProfilesFunc = func(ctx context.Context, hostUUID string) ([]automdm.HostMDMAppleProfile, error) {
				if c.hostRow == nil {
					return nil, nil
				}
				return []automdm.HostMDMAppleProfile{*c.hostRow}, nil
			}
			ds.GetAppleProfileForReconcileFunc = func(ctx context.Context, teamID uint, profileUUID string) (*automdm.AppleProfileForReconcile, error) {
				return c.profile, nil
			}
			ds.BulkGetHostLabelMembershipsFunc = func(ctx context.Context, hostIDs, labelIDs []uint) (map[uint]map[uint]struct{}, error) {
				m := map[uint]struct{}{}
				for _, id := range c.members {
					m[id] = struct{}{}
				}
				return map[uint]map[uint]struct{}{c.host.ID: m}, nil
			}
			var applied *automdm.MDMProfileOptInChanges
			ds.ApplyHostMDMProfileOptInChangesFunc = func(ctx context.Context, changes *automdm.MDMProfileOptInChanges) error {
				applied = changes
				return nil
			}
			var queuedInstall *automdm.AppleProfileForReconcile
			ds.QueueHostMDMAppleProfileInstallFunc = func(ctx context.Context, hostUUID string, profile *automdm.AppleProfileForReconcile) error {
				require.Equal(t, macHost.UUID, hostUUID)
				queuedInstall = profile
				return c.queueErr
			}
			var queuedRemoval string
			ds.QueueHostMDMAppleProfileRemovalFunc = func(ctx context.Context, hostUUID, profileUUID string) error {
				require.Equal(t, macHost.UUID, hostUUID)
				queuedRemoval = profileUUID
				return c.queueErr
			}

			handle := svc.handleInstallSelfServiceConfigurationProfile
			if c.uninstall {
				handle = svc.handleUninstallSelfServiceConfigurationProfile
			}
			name, err := handle(ctx, c.host, "prof-uuid")

			if c.wantErr != nil {
				require.ErrorAs(t, err, reflect.New(reflect.TypeOf(c.wantErr)).Interface())
				require.Nil(t, applied)
				require.False(t, ds.QueueHostMDMAppleProfileInstallFuncInvoked)
				require.False(t, ds.QueueHostMDMAppleProfileRemovalFuncInvoked)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "SS", name)
			want := []automdm.HostProfileUUID{{HostUUID: macHost.UUID, ProfileUUID: "prof-uuid"}}
			if c.uninstall {
				require.Equal(t, &automdm.MDMProfileOptInChanges{Purge: want}, applied)
				require.Equal(t, "prof-uuid", queuedRemoval)
				require.False(t, ds.QueueHostMDMAppleProfileInstallFuncInvoked)
			} else {
				require.Equal(t, &automdm.MDMProfileOptInChanges{Add: want}, applied)
				require.Same(t, c.profile, queuedInstall)
				require.False(t, ds.QueueHostMDMAppleProfileRemovalFuncInvoked)
			}
		})
	}
}
