package service

import (
	"errors"
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// CreateAPIOnlyUser creates a new API-only user, returning the API token for the new user.
func (c *Client) CreateAPIOnlyUser(name string, globalRole *string, teams []automdm.UserTeam) (*string, error) {
	verb, path := "POST", "/api/latest/automdm/users/api_only"
	payload := automdm.UserPayload{
		Name:       &name,
		GlobalRole: globalRole,
		Teams:      &teams,
	}
	var responseBody createUserResponse
	if err := c.authenticatedRequest(payload, verb, path, &responseBody); err != nil {
		return nil, err
	}
	return responseBody.Token, nil
}

// CreateUser creates a new user, skipping the invitation process.
//
// The session key (aka API token) is returned only when creating
// API only users.
func (c *Client) CreateUser(p automdm.UserPayload) (*string, error) {
	verb, path := "POST", "/api/latest/automdm/users/admin"
	var responseBody createUserResponse
	if err := c.authenticatedRequest(p, verb, path, &responseBody); err != nil {
		return nil, err
	}
	return responseBody.Token, nil
}

// ListUsers retrieves the list of users.
func (c *Client) ListUsers() ([]automdm.User, error) {
	verb, path := "GET", "/api/latest/automdm/users"
	var responseBody listUsersResponse

	err := c.authenticatedRequest(nil, verb, path, &responseBody)
	if err != nil {
		return nil, err
	}
	return responseBody.Users, nil
}

func (c *Client) userIdFromEmail(email string) (uint, error) {
	verb, path := "POST", "/api/latest/automdm/translate"
	var responseBody translatorResponse

	params := translatorRequest{List: []automdm.TranslatePayload{
		{
			Type:    automdm.TranslatorTypeUserEmail,
			Payload: automdm.StringIdentifierToIDPayload{Identifier: email},
		},
	}}

	err := c.authenticatedRequest(&params, verb, path, &responseBody)
	if err != nil {
		return 0, err
	}
	if len(responseBody.List) != 1 {
		return 0, errors.New("Expected 1 item translated, got none")
	}
	return responseBody.List[0].Payload.ID, nil
}

// DeleteUser deletes the user specified by the email
func (c *Client) DeleteUser(email string) error {
	userID, err := c.userIdFromEmail(email)
	if err != nil {
		return err
	}

	verb, path := "DELETE", fmt.Sprintf("/api/latest/automdm/users/%d", userID)
	var responseBody deleteUserResponse
	return c.authenticatedRequest(nil, verb, path, &responseBody)
}

// Me returns the user associated with the current session.
func (c *Client) Me() (*automdm.User, error) {
	verb, path := "GET", "/api/latest/automdm/me"
	var responseBody getUserResponse
	err := c.authenticatedRequest(nil, verb, path, &responseBody)
	return responseBody.User, err
}
