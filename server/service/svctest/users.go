// Package svctest provides test helpers (test server, test service, login
// helper) for the server/service package. It imports the testing package and
// must therefore only ever be imported from test code; importing it from
// production code would pull "testing" into the resulting binary.
package svctest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/ptr"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testUsers is the set of users seeded into the database when a test server
// is created. Internal service tests have their own copy with the same
// content; both must stay in sync.
var testUsers = map[string]struct {
	Email             string
	PlaintextPassword string
	GlobalRole        *string
}{
	"admin1": {
		PlaintextPassword: test.GoodPassword,
		Email:             service.TestAdminUserEmail,
		GlobalRole:        ptr.String(automdm.RoleAdmin),
	},
	"user1": {
		PlaintextPassword: test.GoodPassword,
		Email:             service.TestMaintainerUserEmail,
		GlobalRole:        ptr.String(automdm.RoleMaintainer),
	},
	"user2": {
		PlaintextPassword: test.GoodPassword,
		Email:             service.TestObserverUserEmail,
		GlobalRole:        ptr.String(automdm.RoleObserver),
	},
}

// createTestUsers seeds the standard set of admin/maintainer/observer users
// into ds and returns them keyed by email.
func createTestUsers(t *testing.T, ds automdm.Datastore) map[string]automdm.User {
	users := make(map[string]automdm.User)
	// Map iteration is random so we sort and iterate using the testUsers keys.
	var keys []string
	for key := range testUsers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	userID := uint(1)
	for _, key := range keys {
		u := testUsers[key]
		user := &automdm.User{
			ID:         userID,
			Name:       "Test Name " + u.Email,
			Email:      u.Email,
			GlobalRole: u.GlobalRole,
		}
		err := user.SetPassword(u.PlaintextPassword, 10, 10)
		require.NoError(t, err)
		user, err = ds.NewUser(context.Background(), user)
		require.NoError(t, err)
		users[user.Email] = *user
		userID++
	}
	return users
}

// GetToken posts to /api/latest/automdm/login and returns the auth token. It
// fails the test on any error.
func GetToken(t *testing.T, email string, password string, serverURL string) string {
	params := automdm.LoginRequest{
		Email:    email,
		Password: password,
	}
	j, err := json.Marshal(&params) //nolint:gosec // dismiss G117
	require.NoError(t, err)

	requestBody := io.NopCloser(bytes.NewBuffer(j))
	resp, err := http.Post(serverURL+"/api/latest/automdm/login", "application/json", requestBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	jsn := struct {
		User  *automdm.User       `json:"user"`
		Token string              `json:"token"`
		Err   []map[string]string `json:"errors,omitempty"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&jsn)
	require.NoError(t, err)
	require.Empty(t, jsn.Err)

	return jsn.Token
}
