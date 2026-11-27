package tests

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/test/httptest"
	"github.com/stretchr/testify/require"
)

func (ts *WithServer) DoJSON(verb, path string, params interface{}, expectedStatusCode int, v interface{}, queryParams ...string) {
	resp := ts.Do(verb, path, params, expectedStatusCode, queryParams...)
	err := json.UnmarshalRead(resp.Body, v)
	require.NoError(ts.T(), err)
	if e, ok := v.(automdm.Errorer); ok {
		require.NoError(ts.T(), e.Error())
	}
}

func (ts *WithServer) Do(verb, path string, params interface{}, expectedStatusCode int, queryParams ...string) *http.Response {
	j, err := json.Marshal(params)
	require.NoError(ts.T(), err)

	resp := ts.DoRaw(verb, path, j, expectedStatusCode, queryParams...)

	ts.T().Cleanup(func() {
		resp.Body.Close()
	})
	return resp
}

func (ts *WithServer) DoRaw(verb string, path string, rawBytes []byte, expectedStatusCode int, queryParams ...string) *http.Response {
	return ts.DoRawWithHeaders(verb, path, rawBytes, expectedStatusCode, map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", ts.Token),
	}, queryParams...)
}

func (ts *WithServer) DoRawWithHeaders(
	verb string, path string, rawBytes []byte, expectedStatusCode int, headers map[string]string, queryParams ...string,
) *http.Response {
	opts := []automdmhttp.ClientOpt{automdmhttp.WithNoTimeout()}
	if expectedStatusCode >= 300 && expectedStatusCode <= 399 {
		opts = append(opts, automdmhttp.WithFollowRedir(false))
	}
	client := automdmhttp.NewClient(opts...)
	return httptest.DoHTTPReq(ts.T(), client, decodeJSON, verb, rawBytes, ts.Server.URL+path, headers, expectedStatusCode, queryParams...)
}

func decodeJSON(r io.Reader, v interface{}) error {
	return json.UnmarshalRead(r, v)
}
