package mysql

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/contexts/ctxerr"
	"github.com/jmoiron/sqlx"
)

// GetLatestCronStats returns a slice of no more than two cron stats records, where index 0 (if
// present) is the most recently created scheduled run, and index 1 (if present) represents a
// triggered run that is currently pending/queued.
func (ds *Datastore) GetLatestCronStats(ctx context.Context, name string) ([]automdm.CronStats, error) {
	stmt := `
(
	SELECT
		id, name, instance, stats_type, status, created_at, updated_at
	FROM
		cron_stats
	WHERE
		name = ?
		AND stats_type = 'scheduled'
		AND (status = 'pending' OR status = 'completed')
	ORDER BY
		created_at DESC
	LIMIT 1)
UNION
(
	SELECT
		id, name, instance, stats_type, status, created_at, updated_at
	FROM
		cron_stats
	WHERE
		name = ?
		AND stats_type = 'triggered'
		AND (status = 'pending' OR status = 'completed' OR status = 'queued')
	ORDER BY
		created_at DESC
	LIMIT 1)`

	var res []automdm.CronStats
	err := sqlx.SelectContext(ctx, ds.reader(ctx), &res, stmt, name, name)
	if err != nil {
		return []automdm.CronStats{}, ctxerr.Wrap(ctx, err, "select cron stats")
	}

	return res, nil
}

func (ds *Datastore) InsertCronStats(ctx context.Context, statsType automdm.CronStatsType, name string, instance string, status automdm.CronStatsStatus) (int, error) {
	stmt := `INSERT INTO cron_stats (stats_type, name, instance, status) VALUES (?, ?, ?, ?)`

	res, err := ds.writer(ctx).ExecContext(ctx, stmt, statsType, name, instance, status)
	if err != nil {
		return 0, ctxerr.Wrap(ctx, err, "insert cron stats")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, ctxerr.Wrap(ctx, err, "insert cron stats last insert id")
	}

	return int(id), nil
}

func (ds *Datastore) UpdateCronStats(ctx context.Context, id int, status automdm.CronStatsStatus, cronErrors *automdm.CronScheduleErrors) error {
	stmt := `UPDATE cron_stats SET status = ?, errors = ? WHERE id = ?`

	errorsJSON := sql.NullString{}
	if len(*cronErrors) > 0 {
		b, err := json.Marshal(cronErrors)
		if err == nil {
			errorsJSON.String = string(b)
			errorsJSON.Valid = true
		}
	}

	if _, err := ds.writer(ctx).ExecContext(ctx, stmt, status, errorsJSON, id); err != nil {
		return ctxerr.Wrap(ctx, err, "update cron stats")
	}

	return nil
}

func (ds *Datastore) ClaimCronStats(ctx context.Context, id int, instance string, status automdm.CronStatsStatus) error {
	stmt := `UPDATE cron_stats SET status = ?, instance = ? WHERE id = ?`

	if _, err := ds.writer(ctx).ExecContext(ctx, stmt, status, instance, id); err != nil {
		return ctxerr.Wrap(ctx, err, "claim cron stats")
	}

	return nil
}

func (ds *Datastore) UpdateAllCronStatsForInstance(ctx context.Context, instance string, fromStatus automdm.CronStatsStatus, toStatus automdm.CronStatsStatus) error {
	stmt := `UPDATE cron_stats SET status = ? WHERE instance = ? AND status = ?`

	if _, err := ds.writer(ctx).ExecContext(ctx, stmt, toStatus, instance, fromStatus); err != nil {
		return ctxerr.Wrap(ctx, err, "update all cron stats for instance")
	}

	return nil
}

func (ds *Datastore) CleanupCronStats(ctx context.Context) error {
	return ds.withRetryTxx(ctx, func(tx sqlx.ExtContext) error {
		// Delete cron_stats entries that are older than two days.
		deleteStmt := `DELETE FROM cron_stats WHERE created_at < DATE_SUB(NOW(), INTERVAL 2 DAY)`
		if _, err := tx.ExecContext(ctx, deleteStmt); err != nil {
			return ctxerr.Wrap(ctx, err, "deleting old cron stats")
		}
		// Mark cron_stats entries as expired if:
		// 1. Pending or queued for >2 hours and no active lock (instance likely crashed), OR
		// 2. Pending or queued for >12 hours regardless of lock state (hard cap for hung jobs).
		//
		// NOTE: The lock check assumes locks.name matches cron_stats.name. Schedules using
		// WithAltLockID (e.g., "leader", "worker") store locks under a different name, so
		// the NOT EXISTS check won't find their lock and they fall back to the 2-hour timeout.
		updateStmt := `
			UPDATE cron_stats cs
			SET cs.status = ?
			WHERE cs.status IN (?, ?)
			AND (
				(cs.created_at < DATE_SUB(NOW(), INTERVAL 2 HOUR)
				AND NOT EXISTS (
					SELECT 1 FROM locks l
					WHERE l.name = cs.name
					AND l.expires_at >= CURRENT_TIMESTAMP
				))
				OR cs.created_at < DATE_SUB(NOW(), INTERVAL 12 HOUR)
			)`
		if _, err := tx.ExecContext(ctx, updateStmt, automdm.CronStatsStatusExpired, automdm.CronStatsStatusPending, automdm.CronStatsStatusQueued); err != nil {
			return ctxerr.Wrap(ctx, err, "updating expired cron stats")
		}

		return nil
	})
}
