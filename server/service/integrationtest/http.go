package integrationtest

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/test/httptest"
	"github.com/stretchr/testify/require"
)

func (s *BaseSuite) DoJSON(t *testing.T, verb, path string, params interface{}, expectedStatusCode int, v interface{}, queryParams ...string) {
	resp := s.Do(t, verb, path, params, expectedStatusCode, queryParams...)
	err := json.UnmarshalRead(resp.Body, v)
	require.NoError(t, err)
	if e, ok := v.(automdm.Errorer); ok {
		require.NoError(t, e.Error())
	}
}

func (s *BaseSuite) Do(t *testing.T, verb, path string, params interface{}, expectedStatusCode int, queryParams ...string) *http.Response {
	j, err := json.Marshal(params)
	require.NoError(t, err)

	resp := s.DoRaw(t, verb, path, j, expectedStatusCode, queryParams...)

	t.Cleanup(func() {
		resp.Body.Close()
	})
	return resp
}

func (s *BaseSuite) DoRaw(t *testing.T, verb string, path string, rawBytes []byte, expectedStatusCode int, queryParams ...string) *http.Response {
	return s.DoRawWithHeaders(t, verb, path, rawBytes, expectedStatusCode, map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", s.Token),
	}, queryParams...)
}

func (s *BaseSuite) DoRawWithHeaders(
	t *testing.T, verb string, path string, rawBytes []byte, expectedStatusCode int, headers map[string]string, queryParams ...string,
) *http.Response {
	opts := []automdmhttp.ClientOpt{automdmhttp.WithNoTimeout()}
	if expectedStatusCode >= 300 && expectedStatusCode <= 399 {
		opts = append(opts, automdmhttp.WithFollowRedir(false))
	}
	client := automdmhttp.NewClient(opts...)
	return httptest.DoHTTPReq(t, client, decodeJSON, verb, rawBytes, s.Server.URL+path, headers, expectedStatusCode, queryParams...)
}

func decodeJSON(r io.Reader, v interface{}) error {
	return json.UnmarshalRead(r, v)
}
