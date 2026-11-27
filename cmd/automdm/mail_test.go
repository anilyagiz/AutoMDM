package main

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/assert"
)

func TestShouldForceSMTPBackend(t *testing.T) {
	smtpOn := &automdm.AppConfig{SMTPSettings: &automdm.SMTPSettings{SMTPEnabled: true}}
	smtpOff := &automdm.AppConfig{SMTPSettings: &automdm.SMTPSettings{SMTPEnabled: false}}

	for _, tc := range []struct {
		name    string
		appCfg  *automdm.AppConfig
		backend string
		want    bool
	}{
		{name: "smtp enabled and custom backend set forces smtp", appCfg: smtpOn, backend: "ses", want: true},
		{name: "smtp enabled but no custom backend", appCfg: smtpOn, backend: "", want: false},
		{name: "smtp disabled with custom backend", appCfg: smtpOff, backend: "ses", want: false},
		{name: "nil smtp settings", appCfg: &automdm.AppConfig{}, backend: "ses", want: false},
		{name: "nil app config", appCfg: nil, backend: "ses", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldForceSMTPBackend(tc.appCfg, tc.backend))
		})
	}
}
