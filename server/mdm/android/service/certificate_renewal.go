package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
)

// RenewCertificateTemplates identifies Android certificate templates that are approaching
// expiration and marks them for renewal by updating their status.
func RenewCertificateTemplates(ctx context.Context, ds automdm.Datastore, logger *slog.Logger) error {
	const batchSize = 1000
	templates, err := ds.GetAndroidCertificateTemplatesForRenewal(ctx, time.Now(), batchSize)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "get android certificate templates for renewal")
	}
	if len(templates) == 0 {
		return nil
	}
	if err := ds.SetAndroidCertificateTemplatesForRenewal(ctx, templates); err != nil {
		return ctxerr.Wrap(ctx, err, "set android certificate templates for renewal")
	}
	logger.InfoContext(ctx, "marked android certificate templates for renewal", "count", len(templates))
	return nil
}
