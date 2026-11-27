package tables

import (
	"database/sql"
	"fmt"
)

func init() {
	MigrationClient.AddMigration(Up_20261112132606, Down_20261112132606)
}

// Up_20261112132606 removes $AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP, which AutoMDM 5 no longer supports. The foreign key deletes its links to
// saved profiles, app configurations, and certificate templates. Those keep the variable in their content and fail on their next delivery.
func Up_20261112132606(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM automdm_variables WHERE name = 'AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP'`); err != nil {
		return fmt.Errorf("deleting automdm variable HOST_END_USER_EMAIL_IDP: %w", err)
	}
	return nil
}

func Down_20261112132606(tx *sql.Tx) error {
	return nil
}
