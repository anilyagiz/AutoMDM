package tables

import (
	"database/sql"
)

func init() {
	MigrationClient.AddMigration(Up_20261111205515, Down_20261111205515)
}

// Up_20261111205515 is a no-op in AutoMDM. Upstream it moved the stored ABM and VPP team assignments from the "team" keys to
// "fleet" keys. AutoMDM kept the "team" keys, so there is nothing to move, and running the upstream copy-then-delete logic with
// equal old and new keys would delete the assignments. The migration stays registered so the version sequence is unchanged.
func Up_20261111205515(tx *sql.Tx) error {
	return nil
}

func Down_20261111205515(tx *sql.Tx) error {
	return nil
}
