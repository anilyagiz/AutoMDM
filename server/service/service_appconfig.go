package service

import (
	"context"
	"errors"
	"strings"

	"github.com/anilyagiz/automdm/v4/server"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	authz_ctx "github.com/anilyagiz/automdm/v4/server/contexts/authz"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxdb"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/contexts/viewer"
	"github.com/anilyagiz/automdm/v4/server/mail"
)

func (svc *Service) NewAppConfig(ctx context.Context, p automdm.AppConfig) (*automdm.AppConfig, error) {
	// skipauth: No user context yet when the app config is first created.
	svc.authz.SkipAuthorization(ctx)

	newConfig, err := svc.ds.NewAppConfig(ctx, &p)
	if err != nil {
		return nil, err
	}

	// Set up a default enroll secret
	secret, err := server.GenerateRandomText(automdm.EnrollSecretDefaultLength)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "generate enroll secret string")
	}
	secrets := []*automdm.EnrollSecret{
		{
			Secret: secret,
		},
	}
	err = svc.ds.ApplyEnrollSecrets(ctx, nil, secrets)
	if err != nil {
		return nil, ctxerr.Wrap(ctx, err, "save enroll secret")
	}

	return newConfig, nil
}

func (svc *Service) sendTestEmail(ctx context.Context, config *automdm.AppConfig) error {
	vc, ok := viewer.FromContext(ctx)
	if !ok {
		return automdm.ErrNoContext
	}

	var smtpSettings automdm.SMTPSettings
	if config.SMTPSettings != nil {
		smtpSettings = *config.SMTPSettings
	}

	testMail := automdm.Email{
		Subject: "Hello from AutoMDM",
		To:      []string{vc.User.Email},
		Mailer: &mail.SMTPTestMailer{
			BaseURL:  emailLinkBaseURL(config.ServerSettings.ServerURL, svc.config.Server.URLPrefix),
			AssetURL: getAssetURL(),
		},
		SMTPSettings: smtpSettings,
		ServerURL:    config.ServerSettings.ServerURL,
	}

	if err := mail.Test(svc.mailService, testMail); err != nil {
		if errors.Is(err, mail.ErrSTARTTLSWithoutSSLTLS) {
			return mail.ErrSTARTTLSWithoutSSLTLS
		}
		return MailError{Message: err.Error()}
	}
	return nil
}

func cleanupURL(url string) string {
	return strings.TrimRight(strings.Trim(url, " \t\n"), "/")
}

func (svc *Service) License(ctx context.Context) (*automdm.LicenseInfo, error) {
	if !svc.authz.IsAuthenticatedWith(ctx, authz_ctx.AuthnDeviceToken) &&
		!svc.authz.IsAuthenticatedWith(ctx, authz_ctx.AuthnDeviceURL) {
		if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
			return nil, err
		}
	}

	licChecker, _ := license.FromContext(ctx)
	// Type assert to get the concrete type to return.
	lic, _ := licChecker.(*automdm.LicenseInfo)

	return lic, nil
}

func (svc *Service) SetupRequired(ctx context.Context) (bool, error) {
	// A lagging replica would report no users and re-open unauthenticated setup.
	hasUsers, err := svc.ds.HasUsers(ctxdb.RequirePrimary(ctx, true))
	if err != nil {
		return false, err
	}
	return !hasUsers, nil
}

func (svc *Service) UpdateIntervalConfig(ctx context.Context) (*automdm.UpdateIntervalConfig, error) {
	return &automdm.UpdateIntervalConfig{
		OSQueryDetail: svc.config.Osquery.DetailUpdateInterval,
		OSQueryPolicy: svc.config.Osquery.PolicyUpdateInterval,
	}, nil
}

func (svc *Service) VulnerabilitiesConfig(ctx context.Context) (*automdm.VulnerabilitiesConfig, error) {
	return &automdm.VulnerabilitiesConfig{
		DatabasesPath:               svc.config.Vulnerabilities.DatabasesPath,
		Periodicity:                 svc.config.Vulnerabilities.Periodicity,
		CPEDatabaseURL:              svc.config.Vulnerabilities.CPEDatabaseURL,
		CPETranslationsURL:          svc.config.Vulnerabilities.CPETranslationsURL,
		CVEFeedPrefixURL:            svc.config.Vulnerabilities.CVEFeedPrefixURL,
		CurrentInstanceChecks:       svc.config.Vulnerabilities.CurrentInstanceChecks,
		DisableDataSync:             svc.config.Vulnerabilities.DisableDataSync,
		RecentVulnerabilityMaxAge:   svc.config.Vulnerabilities.RecentVulnerabilityMaxAge,
		DisableWinOSVulnerabilities: svc.config.Vulnerabilities.DisableWinOSVulnerabilities,
		OSVForVulnerabilities:       svc.config.Vulnerabilities.OSVForVulnerabilities,
	}, nil
}

func (svc *Service) LoggingConfig(ctx context.Context) (*automdm.Logging, error) {
	conf := svc.config
	logging := &automdm.Logging{
		Debug: conf.Logging.Debug,
		Json:  conf.Logging.JSON,
	}

	loggings := []struct {
		plugin string
		target *automdm.LoggingPlugin
	}{
		{
			plugin: conf.Osquery.StatusLogPlugin,
			target: &logging.Status,
		},
		{
			plugin: conf.Osquery.ResultLogPlugin,
			target: &logging.Result,
		},
	}

	if conf.Activity.EnableAuditLog {
		loggings = append(loggings, struct {
			plugin string
			target *automdm.LoggingPlugin
		}{
			plugin: conf.Activity.AuditLogPlugin,
			target: &logging.Audit,
		})
	}

	for _, lp := range loggings {
		switch lp.plugin {
		case "", "filesystem":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "filesystem",
				Config: automdm.FilesystemConfig{
					FilesystemConfig: conf.Filesystem,
				},
			}
		case "webhook":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "webhook",
				Config: automdm.WebhookConfig{
					WebhookConfig: conf.Webhook,
				},
			}
		case "kinesis":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "kinesis",
				Config: automdm.KinesisConfig{
					Region:       conf.Kinesis.Region,
					StatusStream: conf.Kinesis.StatusStream,
					ResultStream: conf.Kinesis.ResultStream,
					AuditStream:  conf.Kinesis.AuditStream,
				},
			}
		case "firehose":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "firehose",
				Config: automdm.FirehoseConfig{
					Region:       conf.Firehose.Region,
					StatusStream: conf.Firehose.StatusStream,
					ResultStream: conf.Firehose.ResultStream,
					AuditStream:  conf.Firehose.AuditStream,
				},
			}
		case "lambda":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "lambda",
				Config: automdm.LambdaConfig{
					Region:         conf.Lambda.Region,
					StatusFunction: conf.Lambda.StatusFunction,
					ResultFunction: conf.Lambda.ResultFunction,
					AuditFunction:  conf.Lambda.AuditFunction,
				},
			}
		case "pubsub":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "pubsub",
				Config: automdm.PubSubConfig{
					PubSubConfig: conf.PubSub,
				},
			}
		case "stdout":
			*lp.target = automdm.LoggingPlugin{Plugin: "stdout"}
		case "kafkarest":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "kafkarest",
				Config: automdm.KafkaRESTConfig{
					StatusTopic: conf.KafkaREST.StatusTopic,
					ResultTopic: conf.KafkaREST.ResultTopic,
					AuditTopic:  conf.KafkaREST.AuditTopic,
					ProxyHost:   conf.KafkaREST.ProxyHost,
				},
			}
		case "nats":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "nats",
				Config: automdm.NatsConfig{
					StatusSubject: conf.Nats.StatusSubject,
					ResultSubject: conf.Nats.ResultSubject,
					AuditSubject:  conf.Nats.AuditSubject,
					Server:        conf.Nats.Server,
				},
			}
		case "splunk":
			*lp.target = automdm.LoggingPlugin{
				Plugin: "splunk",
				Config: automdm.SplunkConfig{
					URL:        conf.Splunk.URL,
					Index:      conf.Splunk.Index,
					Source:     conf.Splunk.Source,
					SourceType: conf.Splunk.SourceType,
				},
			}
		default:
			return nil, ctxerr.Errorf(ctx, "unrecognized logging plugin: %s", lp.plugin)
		}
	}
	return logging, nil
}

func (svc *Service) EmailConfig(ctx context.Context) (*automdm.EmailConfig, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
		return nil, err
	}

	conf := svc.config
	var email *automdm.EmailConfig
	switch conf.Email.EmailBackend {
	case "ses":
		email = &automdm.EmailConfig{
			Backend: conf.Email.EmailBackend,
			Config: automdm.SESConfig{
				Region:       conf.SES.Region,
				SourceARN:    conf.SES.SourceArn,
				SenderDomain: conf.SES.SenderDomain,
			},
		}
	default:
		// SES is the only email provider configured as server envs/yaml file, the default implementation, SMTP, is configured via API/UI
		// SMTP config gets its own dedicated section in the AppConfig response
	}

	return email, nil
}

func (svc *Service) PartnershipsConfig(ctx context.Context) (*automdm.Partnerships, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
		return nil, err
	}
	enablePrimo := svc.config.Partnerships.EnablePrimo
	if !enablePrimo {
		// for now, since this is the only partnership of this type, exclude the whole struct if not enabled
		return nil, nil
	}
	return &automdm.Partnerships{
		EnablePrimo: svc.config.Partnerships.EnablePrimo,
	}, nil
}

func (svc *Service) AuthSettings(ctx context.Context) (*automdm.AuthSettings, error) {
	if err := svc.authz.Authorize(ctx, &automdm.AppConfig{}, automdm.ActionRead); err != nil {
		return nil, err
	}
	if !svc.config.MDM.OneTimeEnrollSecretsEnabled() {
		// Like Partnerships, omit the whole object while nothing in it is enabled.
		return nil, nil
	}
	return &automdm.AuthSettings{
		MDMAppleOneTimeEnrollSecrets:   svc.config.MDM.AppleOneTimeEnrollSecrets,
		MDMWindowsOneTimeEnrollSecrets: svc.config.MDM.WindowsOneTimeEnrollSecrets,
		MDMAndroidOneTimeEnrollSecrets: svc.config.MDM.AndroidOneTimeEnrollSecrets,
	}, nil
}
