package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/retry"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/rs/zerolog/log"
)

// DeviceClient is used consume the `device/...` endpoints and meant to be used by AutoMDM Desktop
type DeviceClient struct {
	*BaseClient

	// automdmAlternativeBrowserHostFromServer serves a similar purpose as automdmAlternativeBrowserHost, but this value is set
	// on the AutoMDM server and takes precedence over it.
	automdmAlternativeBrowserHostFromServer string

	// automdmAlternativeBrowserHost is an alternative host to use for the AutoMDM Desktop URLs generated for the browser.
	//
	// This is needed when the host that Orbit will connect to is different from the host that will connect via the browser.
	automdmAlternativeBrowserHost string

	// if set and a request fails with ErrUnauthenticated, the client will call
	// this function to get a fresh token and retry if it returns a different,
	// non-empty token.
	invalidTokenRetryFunc func() string
}

// NewDeviceClient instantiates a new client to perform requests against device endpoints.
func NewDeviceClient(addr string, insecureSkipVerify bool, rootCA string, automdmClientCrt *tls.Certificate, automdmAlternativeBrowserHost string) (*DeviceClient, error) {
	capabilities := automdm.CapabilityMap{}
	baseClient, err := NewBaseClient(addr, insecureSkipVerify, rootCA, "", automdmClientCrt, capabilities, nil)
	if err != nil {
		return nil, err
	}

	return &DeviceClient{
		BaseClient:                    baseClient,
		automdmAlternativeBrowserHost: automdmAlternativeBrowserHost,
	}, nil
}

// WithInvalidTokenRetry sets the function to call if a request fails with ErrUnauthenticated.
func (dc *DeviceClient) WithInvalidTokenRetry(fn func() string) {
	log.Debug().Msg("setting invalid token retry hook")
	dc.invalidTokenRetryFunc = fn
}

// request performs the request, resolving the pathFmt that should contain a %s
// verb to be replaced with the token, or no verb at all if the token is "-"
// (the pathFmt is used as-is as path). It will retry if the request fails due
// to an invalid token and the invalidTokenRetryFunc field is set.
func (dc *DeviceClient) request(verb, pathFmt, token, query string, params any, responseDest any) error {
	const maxAttempts = 4
	var attempt int
	for {
		attempt++

		path := pathFmt
		if token != "-" {
			path = fmt.Sprintf(pathFmt, token)
		}
		reqErr := dc.requestAttempt(verb, path, query, params, responseDest)
		if attempt >= maxAttempts || dc.invalidTokenRetryFunc == nil || token == "-" || !errors.Is(reqErr, ErrUnauthenticated) {
			// no retry possible, return the result
			if reqErr != nil {
				log.Debug().Msgf("not retrying API error; attempt=%d, hook set=%t, token unset=%t, error is auth=%t",
					attempt, dc.invalidTokenRetryFunc != nil, token == "-", errors.Is(reqErr, ErrUnauthenticated))
			}
			return reqErr
		}

		delay := time.Duration(attempt) * time.Second
		log.Debug().Msgf("retrying API error in %s", delay)
		time.Sleep(delay)
		newToken := dc.invalidTokenRetryFunc()
		log.Debug().Msgf("retrying API error; token is different=%t", newToken != "" && newToken != token)
		if newToken != "" {
			token = newToken
		}
	}
}

func (dc *DeviceClient) requestAttempt(verb string, path string, query string, params any, responseDest any) error {
	var bodyBytes []byte
	var err error
	if params != nil {
		bodyBytes, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("making request json marshalling : %w", err)
		}
	}
	requestURL, err := dc.RequestURL(path, query)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(
		verb,
		requestURL,
		bytes.NewBuffer(bodyBytes),
	)
	if err != nil {
		return err
	}

	dc.SetClientCapabilitiesHeader(request)
	response, err := dc.DoHTTPRequest(request)
	if err != nil {
		return fmt.Errorf("%s %s: %w", verb, path, err)
	}
	defer response.Body.Close()

	return dc.ParseResponse(verb, path, response, responseDest)
}

// getAlternativeBrowserHostSetting overrides portions of the provided URL based on the values of
// automdmAlternativeBrowserHostFromServer or automdmAlternativeBrowserHost
func (dc *DeviceClient) getAlternativeBrowserHostSetting() string {
	if dc.automdmAlternativeBrowserHostFromServer != "" {
		return dc.automdmAlternativeBrowserHostFromServer
	}
	return dc.automdmAlternativeBrowserHost
}

// BrowserTransparencyURL returns a URL for the browser that the server
// will use to redirect to the transparency URL configured by the user.
func (dc *DeviceClient) BrowserTransparencyURL(token string) string {
	transparencyURL := dc.BaseClient.URL("/api/latest/automdm/device/"+token+"/transparency", "")
	if altHost := dc.getAlternativeBrowserHostSetting(); altHost != "" {
		transparencyURL.Host = altHost
	}
	return transparencyURL.String()
}

// BrowserSelfServiceURL returns the "Self-service" URL for the browser.
func (dc *DeviceClient) BrowserSelfServiceURL(token string) string {
	selfServiceURL := dc.BaseClient.URL("/device/"+token+"/self-service", "")
	if altHost := dc.getAlternativeBrowserHostSetting(); altHost != "" {
		selfServiceURL.Host = altHost
	}
	return selfServiceURL.String()
}

// BrowserDeviceURL returns the "My device" URL for the browser.
func (dc *DeviceClient) BrowserDeviceURL(token string) string {
	deviceURL := dc.BaseClient.URL("/device/"+token, "")
	if altHost := dc.getAlternativeBrowserHostSetting(); altHost != "" {
		deviceURL.Host = altHost
	}
	return deviceURL.String()
}

// BrowserPoliciesURL returns the "Policies" URL for the browser.
func (dc *DeviceClient) BrowserPoliciesURL(token string) string {
	policiesURL := dc.BaseClient.URL(fmt.Sprintf(`/device/%s/policies`, token), "")
	if altHost := dc.getAlternativeBrowserHostSetting(); altHost != "" {
		policiesURL.Host = altHost
	}
	return policiesURL.String()
}

// CheckToken checks if a token is valid by making an authenticated request to the server.
func (dc *DeviceClient) CheckToken(token string) error {
	verb, path := "HEAD", "/api/latest/automdm/device/%s/ping"
	err := dc.request(verb, path, token, "", nil, nil)

	if IsNotFoundErr(err) {
		_, err = dc.DesktopSummary(token)
	}
	return err
}

// Ping sends a ping to the server using the device/ping endpoint.
func (dc *DeviceClient) Ping() error {
	verb, path := "HEAD", "/api/automdm/device/ping"
	err := dc.request(verb, path, "-", "", nil, nil)

	if err == nil || IsNotFoundErr(err) {
		return nil
	}

	return err
}

// listDevicePoliciesResponse is a local response type for deserializing the device policies response.
// Definition duplicated for now (orbit should not depend server/service).
type listDevicePoliciesResponse struct {
	Err      error                   `json:"error,omitempty"`
	Policies []*automdm.DevicePolicy `json:"policies"`
}

func (r listDevicePoliciesResponse) Error() error { return r.Err }

func (dc *DeviceClient) getListDevicePolicies(token string) ([]*automdm.DevicePolicy, error) {
	verb, path := "GET", "/api/latest/automdm/device/%s/policies"
	var responseBody listDevicePoliciesResponse
	err := dc.request(verb, path, token, "", nil, &responseBody)
	return responseBody.Policies, err
}

// automdmDesktopResponse is a local response type for deserializing the desktop summary response.
type automdmDesktopResponse struct {
	Err error `json:"error,omitempty"`
	automdm.DesktopSummary
}

func (r automdmDesktopResponse) Error() error { return r.Err }

func (dc *DeviceClient) getMinDesktopPayload(token string) (automdmDesktopResponse, error) {
	verb, path := "GET", "/api/latest/automdm/device/%s/desktop"
	var r automdmDesktopResponse
	err := dc.request(verb, path, token, "", nil, &r)
	return r, err
}

func (dc *DeviceClient) DesktopSummary(token string) (*automdmDesktopResponse, error) {
	r, err := dc.getMinDesktopPayload(token)
	if err == nil {
		r.FailingPolicies = new(uintValueOrZero(r.FailingPolicies))
		dc.automdmAlternativeBrowserHostFromServer = r.AlternativeBrowserHost
		return &r, nil
	}

	if IsNotFoundErr(err) {
		policies, err := dc.getListDevicePolicies(token)
		if err != nil {
			return nil, err
		}

		var failingPolicies uint
		for _, policy := range policies {
			if policy.Response != "pass" {
				failingPolicies++
			}
		}
		return &automdmDesktopResponse{
			DesktopSummary: automdm.DesktopSummary{
				FailingPolicies: new(failingPolicies),
			},
		}, nil
	}

	return nil, err
}

func (dc *DeviceClient) MigrateMDM(token string) error {
	verb, path := "POST", "/api/latest/automdm/device/%s/migrate_mdm"
	return dc.request(verb, path, token, "", nil, nil)
}

// automdmdErrorRequest is a local request type for the error reporting endpoint.
type automdmdErrorRequest struct {
	AutoMDMdError automdm.AutoMDMdError `json:"error"`
}

func (dc *DeviceClient) ReportError(token string, automdmdErr automdm.AutoMDMdError) error {
	verb, path := "POST", "/api/latest/automdm/device/%s/debug/errors"
	req := automdmdErrorRequest{AutoMDMdError: automdmdErr}
	return retry.Do(
		func() error {
			err := dc.request(verb, path, token, "", req, nil)
			if err != nil {
				return err
			}
			return nil
		},
		retry.WithMaxAttempts(3),
		retry.WithInterval(15*time.Second),
	)
}

func uintValueOrZero(v *uint) uint {
	if v == nil {
		return 0
	}
	return *v
}
