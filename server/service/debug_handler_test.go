package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockService struct {
	mock.Mock
	automdm.Service
}

func (m *mockService) GetSessionByKey(ctx context.Context, sessionKey string) (*automdm.Session, error) {
	args := m.Called(ctx, sessionKey)
	if ret := args.Get(0); ret != nil {
		return ret.(*automdm.Session), nil
	}
	return nil, args.Error(1)
}

func (m *mockService) UserUnauthorized(ctx context.Context, userId uint) (*automdm.User, error) {
	args := m.Called(ctx, userId)
	if ret := args.Get(0); ret != nil {
		return ret.(*automdm.User), nil
	}
	return nil, args.Error(1)
}

var testConfig = config.AutoMDMConfig{
	Auth: config.AuthConfig{},
}

func TestDebugHandlerAuthenticationTokenMissing(t *testing.T) {
	handler := MakeDebugHandler(&mockService{}, testConfig, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "https://automdm.com/debug/pprof/profile", nil)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	assert.Equal(t, http.StatusUnauthorized, res.Code)
}

func TestDebugHandlerAuthenticationSessionInvalid(t *testing.T) {
	svc := &mockService{}
	svc.On(
		"GetSessionByKey",
		mock.Anything,
		"fake_session_key",
	).Return(nil, errors.New("invalid session"))

	handler := MakeDebugHandler(svc, testConfig, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "https://automdm.com/debug/pprof/profile", nil)
	req.Header.Add("Authorization", "BEARER fake_session_key")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	assert.Equal(t, http.StatusUnauthorized, res.Code)
}

func TestDebugHandlerAuthenticationFailsDueToRole(t *testing.T) {
	for test, user := range map[string]automdm.User{
		"no role":                {},
		"global observer role":   {GlobalRole: new(automdm.RoleObserver)},
		"global maintainer role": {GlobalRole: new(automdm.RoleMaintainer)},
		"non-global role":        {Teams: []automdm.UserTeam{{Team: automdm.Team{ID: 1, Name: "foo"}, Role: automdm.RoleAdmin}}},
	} {
		t.Run(test, func(t *testing.T) {
			svc := &mockService{}
			svc.On(
				"GetSessionByKey",
				mock.Anything,
				"fake_session_key",
			).Return(&automdm.Session{UserID: 42, ID: 1}, nil)
			svc.On(
				"UserUnauthorized",
				mock.Anything,
				uint(42),
			).Return(&user, nil)

			handler := MakeDebugHandler(svc, testConfig, nil, nil, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "https://automdm.com/debug/pprof/cmdline", nil)
			req.Header.Add("Authorization", "BEARER fake_session_key")
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)
			assert.Equal(t, http.StatusForbidden, res.Code)
		})
	}
}

func TestDebugHandlerAuthenticationFailsForRestrictedAPIOnlyUser(t *testing.T) {
	// A global-admin API-only token scoped to an endpoint allowlist must not reach the debug
	// routes: those routes are not in the public API catalog, so they can never be allowlisted.
	svc := &mockService{}
	svc.On(
		"GetSessionByKey",
		mock.Anything,
		"fake_session_key",
	).Return(&automdm.Session{UserID: 42, ID: 1}, nil)
	svc.On(
		"UserUnauthorized",
		mock.Anything,
		uint(42),
	).Return(&automdm.User{
		GlobalRole:   new(automdm.RoleAdmin),
		APIOnly:      true,
		APIEndpoints: []automdm.APIEndpointRef{{Method: "GET", Path: "/api/v1/automdm/hosts"}},
	}, nil)

	handler := MakeDebugHandler(svc, testConfig, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "https://automdm.com/debug/pprof/cmdline", nil)
	req.Header.Add("Authorization", "BEARER fake_session_key")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	assert.Equal(t, http.StatusForbidden, res.Code)
}

func TestDebugHandlerAuthenticationSucceeds(t *testing.T) {
	// An unrestricted API-only admin (empty APIEndpoints) retains full access, matching the main
	// API path where APIOnlyEndpointCheck is a no-op for tokens with no endpoint restrictions.
	for test, user := range map[string]automdm.User{
		"admin session":            {GlobalRole: new(automdm.RoleAdmin)},
		"unrestricted api-only":    {GlobalRole: new(automdm.RoleAdmin), APIOnly: true},
		"api-only empty allowlist": {GlobalRole: new(automdm.RoleAdmin), APIOnly: true, APIEndpoints: []automdm.APIEndpointRef{}},
	} {
		t.Run(test, func(t *testing.T) {
			svc := &mockService{}
			svc.On(
				"GetSessionByKey",
				mock.Anything,
				"fake_session_key",
			).Return(&automdm.Session{UserID: 42, ID: 1}, nil)
			svc.On(
				"UserUnauthorized",
				mock.Anything,
				uint(42),
			).Return(&user, nil)

			handler := MakeDebugHandler(svc, testConfig, nil, nil, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "https://automdm.com/debug/pprof/cmdline", nil)
			req.Header.Add("Authorization", "BEARER fake_session_key")
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)
			assert.Equal(t, http.StatusOK, res.Code)
		})
	}
}
