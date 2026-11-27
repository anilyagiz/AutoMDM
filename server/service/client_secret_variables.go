package service

import "github.com/anilyagiz/automdm/v4/server/automdm"

func (c *Client) SaveSecretVariables(secretVariables []automdm.SecretVariable, dryRun bool) error {
	verb, path := "PUT", "/api/latest/automdm/spec/secret_variables"
	params := automdm.CreateSecretVariablesRequest{
		SecretVariables: secretVariables,
		DryRun:          dryRun,
	}
	var responseBody automdm.CreateSecretVariablesResponse
	return c.authenticatedRequest(params, verb, path, &responseBody)
}
