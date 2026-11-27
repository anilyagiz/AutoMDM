package mysql

import (
	"context"
	"fmt"
	"strings"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/jmoiron/sqlx"
)

func (ds *Datastore) GetLinuxDiskEncryptionSummary(ctx context.Context, teamID *uint) (automdm.MDMLinuxDiskEncryptionSummary, error) {
	var args []interface{}
	var teamFilter string
	if teamID != nil {
		teamFilter = "AND h.team_id = ?"
		args = append(args, *teamID)
	} else {
		teamFilter = "AND h.team_id IS NULL"
	}

	stmt := fmt.Sprintf(`SELECT
			CASE WHEN hdek.base64_encrypted IS NOT NULL
					AND hdek.base64_encrypted != ''
					AND hdek.client_error = '' THEN
					'verified'
				WHEN hdek.client_error IS NOT NULL
					AND hdek.client_error != '' THEN
					'failed'
				WHEN hdek.base64_encrypted IS NULL
					OR (hdek.base64_encrypted = ''
					AND hdek.client_error = '') THEN
					'action_required'
				END AS status,
				COUNT(h.id) AS host_count
			FROM
				hosts h
				LEFT JOIN host_disk_encryption_keys hdek ON h.id = hdek.host_id
			WHERE
				`+sqlLUKSSupportedHostCond+`
				%s
			GROUP BY
				status`, teamFilter)

	type countRow struct {
		Status    string `db:"status"`
		HostCount uint   `db:"host_count"`
	}

	var counts []countRow
	summary := automdm.MDMLinuxDiskEncryptionSummary{}

	if err := sqlx.SelectContext(ctx, ds.reader(ctx), &counts, stmt, args...); err != nil {
		return summary, err
	}

	for _, count := range counts {
		switch count.Status {
		case "verified":
			summary.Verified = count.HostCount
		case "action_required":
			summary.ActionRequired = count.HostCount
		case "failed":
			summary.Failed = count.HostCount
		}
	}

	return summary, nil
}

var (
	// sqlLUKSSupportedPlatforms is automdm.LUKSSupportedPlatforms as a quoted SQL list.
	sqlLUKSSupportedPlatforms = "'" + strings.Join(automdm.LUKSSupportedPlatforms, "', '") + "'"

	// sqlLUKSSupportedHostCond mirrors automdm.Host.IsLUKSSupported for the hosts table aliased as h.
	// Its % are doubled because every caller embeds it in a fmt format string.
	sqlLUKSSupportedHostCond = `(h.platform IN (` + sqlLUKSSupportedPlatforms + `) OR h.os_version LIKE '%%Fedora%%')`
)

func sqlCaseLinuxOSSettingsStatus() string {
	return `
	CASE WHEN 
		hdek.base64_encrypted IS NOT NULL
		AND hdek.base64_encrypted != ''
		AND hdek.client_error = '' THEN
		'` + string(automdm.OSSettingsVerified) + `'
	WHEN hdek.client_error IS NOT NULL
	AND hdek.client_error != '' THEN
		'` + string(automdm.OSSettingsFailed) + `'
	WHEN hdek.base64_encrypted IS NULL
		OR (hdek.base64_encrypted = ''
		AND hdek.client_error = '') THEN
		'` + string(automdm.OSSettingsPending) + `'
	END`
}

func sqlCaseLinuxDiskEncryptionStatus() string {
	return `
	CASE WHEN 
		hdek.base64_encrypted IS NOT NULL
		AND hdek.base64_encrypted != ''
		AND hdek.client_error = '' THEN
		'` + string(automdm.DiskEncryptionVerified) + `'
	WHEN hdek.client_error IS NOT NULL
	AND hdek.client_error != '' THEN
		'` + string(automdm.DiskEncryptionFailed) + `'
	WHEN hdek.base64_encrypted IS NULL
		OR (hdek.base64_encrypted = ''
		AND hdek.client_error = '') THEN
		'` + string(automdm.DiskEncryptionActionRequired) + `'
	END`
}
