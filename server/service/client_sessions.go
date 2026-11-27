package service

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// Login attempts to login to the current AutoMDM instance. If login is successful,
// an auth token is returned.
func (c *Client) Login(email, password string) (string, error) {
	params := automdm.LoginRequest{
		Email:    email,
		Password: password,
	}

	response, err := c.Do("POST", "/api/latest/automdm/login", "", params)
	if err != nil {
		return "", fmt.Errorf("POST /api/latest/automdm/login: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return "", notSetupErr{}
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"login received status %d %s",
			response.StatusCode,
			extractServerErrorText(response.Body),
		)
	}

	var responseBody automdm.LoginResponse
	err = json.NewDecoder(response.Body).Decode(&responseBody)
	if err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}

	if responseBody.Err != nil {
		return "", fmt.Errorf("login: %s", responseBody.Err)
	}

	return responseBody.Token, nil
}

// SSOSettings returns the SSO settings for the current AutoMDM instance.
// This endpoint is unauthenticated and can be called before login.
func (c *Client) SSOSettings() (*automdm.SessionSSOSettings, error) {
	response, err := c.Do("GET", "/api/v1/automdm/sso", "", nil)
	if err != nil {
		return nil, fmt.Errorf("GET /api/v1/automdm/sso: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get SSO settings received status %d %s", response.StatusCode, extractServerErrorText(response.Body))
	}

	var responseBody struct {
		Settings *automdm.SessionSSOSettings `json:"settings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&responseBody); err != nil {
		return nil, fmt.Errorf("decode SSO settings response: %w", err)
	}

	return responseBody.Settings, nil
}

// Logout attempts to logout to the current AutoMDM instance.
func (c *Client) Logout() error {
	verb, path := "POST", "/api/latest/automdm/logout"
	var responseBody automdm.LogoutResponse
	return c.authenticatedRequest(nil, verb, path, &responseBody)
}
