package service

import (
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/mdm/android"
	eu "github.com/anilyagiz/automdm/v4/server/platform/endpointer"
	"github.com/docker/go-units"
	kithttp "github.com/go-kit/kit/transport/http"
)

func GetRoutes(automdmSvc automdm.Service, svc android.Service) eu.HandlerRoutesFunc {
	return func(r *eu.Router, opts []kithttp.ServerOption) {
		attachAutoMDMAPIRoutes(r, automdmSvc, svc, opts)
	}
}

const pubSubPushPath = "/api/v1/automdm/android_enterprise/pubsub"

func attachAutoMDMAPIRoutes(r *eu.Router, automdmSvc automdm.Service, svc android.Service, opts []kithttp.ServerOption) {
	// //////////////////////////////////////////
	// User-authenticated endpoints
	ue := newUserAuthenticatedEndpointer(automdmSvc, svc, opts, r, apiVersions()...)

	ue.GET("/api/_version_/automdm/android_enterprise/signup_url", enterpriseSignupEndpoint, nil)
	ue.GET("/api/_version_/automdm/android_enterprise", getEnterpriseEndpoint, nil)
	ue.DELETE("/api/_version_/automdm/android_enterprise", deleteEnterpriseEndpoint, nil)
	ue.GET("/api/_version_/automdm/android_enterprise/signup_sse", enterpriseSSE, nil)
	ue.GET("/api/_version_/automdm/android_enterprise/zero_touch_configuration", zeroTouchConfigurationEndpoint, zeroTouchConfigurationRequest{})

	// //////////////////////////////////////////
	// Unauthenticated endpoints
	// These endpoints should do custom one-time authentication by verifying that a valid secret token is provided with the request.
	ne := newNoAuthEndpointer(automdmSvc, svc, opts, r, apiVersions()...)

	ne.GET("/api/_version_/automdm/android_enterprise/connect/{token}", enterpriseSignupCallbackEndpoint, enterpriseSignupCallbackRequest{})
	ne.GET("/api/_version_/automdm/android_enterprise/enrollment_token", enrollmentTokenEndpoint, enrollmentTokenRequest{})
	ne.WithRequestBodySizeLimit(10*units.MiB).POST(pubSubPushPath, pubSubPushEndpoint, PubSubPushRequest{})
}

func apiVersions() []string {
	return []string{"v1"}
}
