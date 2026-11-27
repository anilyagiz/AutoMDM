package tables

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func init() {
	MigrationClient.AddMigration(Up_20261117141344, Down_20261117141344)
}

const teamNameMaxLength20261117141344 = 255

// teamNameKey20261117141344 is a frozen copy of automdm.TeamNameKey, so this migration keeps renaming the same teams
// if that rule changes later.
func teamNameKey20261117141344(name string) string {
	folded := norm.NFKD.String(cases.Fold().String(norm.NFKD.String(name)))
	var key strings.Builder
	// Combining marks are ignored after a Latin letter, so "é" matches "e". After a letter of any other script they are
	// kept, because there a mark can make a different letter or word ("й" and "и", "राम" and "रम"). Pairs the teams.name
	// collation treats as equal ("ё" and "е", "ガ" and "カ") still conflict through the collation check.
	var keepMarks bool
	for _, r := range folded {
		switch {
		case unicode.IsLetter(r):
			key.WriteRune(r)
			keepMarks = !unicode.Is(unicode.Latin, r)
		case unicode.IsNumber(r):
			key.WriteRune(r)
			keepMarks = false
		case unicode.IsMark(r):
			if keepMarks {
				key.WriteRune(r)
			}
		default:
			keepMarks = false
		}
	}
	return key.String()
}

// reservedTeamNames20261117141344 is a frozen copy of the reserved team names, so this migration keeps renaming the
// same teams if that list changes later.
var reservedTeamNames20261117141344 = []string{"Unassigned", "All teams", "No team", "All teams"}

type renamedTeam20261117141344 struct {
	id      uint
	oldName string
	newName string
}

// Up_20261117141344 renames teams that break the AutoMDM 5 name rules: a name needs at least one letter or number, must
// not match another team's name by name key or under the teams.name collation, and must not match a reserved team
// name by name key.
//
// Teams are processed in id order and each rename is written right away, so the teams table always holds the final
// names of the teams already processed and the current names of the rest. A team whose name has no letter or number is
// numbered from 1. A team that matches a name an earlier team ended up with is numbered from 2, so the oldest team
// keeps its name. A team that matches a reserved name is also numbered from 2, including a team named "Unassigned"
// before that name was reserved. A number is skipped if the result would match any name in the table.
func Up_20261117141344(tx *sql.Tx) error {
	type automdmRow struct {
		id   uint
		name string
		key  string
	}
	var teams []automdmRow
	liveKeys := make(map[string]int)
	err := forEachRow20261117141344(tx, `SELECT id, name FROM teams ORDER BY id`, func(rows *sql.Rows) error {
		var f automdmRow
		if err := rows.Scan(&f.id, &f.name); err != nil {
			return err
		}
		f.key = teamNameKey20261117141344(f.name)
		liveKeys[f.key]++
		teams = append(teams, f)
		return nil
	})
	if err != nil {
		return fmt.Errorf("select teams: %w", err)
	}

	// A new name never matches a current name, so a team can only match an earlier team under the collation if it
	// did before the migration. That limits the per-team collation query to these teams.
	matchedEarlierAutoMDM := make(map[uint]struct{})
	err = forEachRow20261117141344(tx, `
		SELECT DISTINCT later.id
		FROM teams later
		JOIN teams earlier ON earlier.name = later.name AND earlier.id < later.id`,
		func(rows *sql.Rows) error {
			var id uint
			if err := rows.Scan(&id); err != nil {
				return err
			}
			matchedEarlierAutoMDM[id] = struct{}{}
			return nil
		})
	if err != nil {
		return fmt.Errorf("select teams matching an earlier team: %w", err)
	}

	nameTaken := func(stmt, name string, id uint) (bool, error) {
		var taken bool
		if err := tx.QueryRow(stmt, name, id).Scan(&taken); err != nil {
			return false, fmt.Errorf("check team name %q under the teams.name collation: %w", name, err)
		}
		return taken, nil
	}

	// A new name ends in a number, so it never matches a reserved name.
	finalKeys := make(map[string]struct{}, len(teams)+len(reservedTeamNames20261117141344))
	for _, name := range reservedTeamNames20261117141344 {
		finalKeys[teamNameKey20261117141344(name)] = struct{}{}
	}
	var renamed []renamedTeam20261117141344
	for _, f := range teams {
		liveKeys[f.key]--

		_, keyTaken := finalKeys[f.key]
		_, mayMatchEarlierAutoMDM := matchedEarlierAutoMDM[f.id]
		firstNumber := 0
		switch {
		case f.key == "":
			firstNumber = 1
		case keyTaken:
			firstNumber = 2
		case mayMatchEarlierAutoMDM:
			taken, err := nameTaken(`SELECT EXISTS (SELECT 1 FROM teams WHERE name = ? AND id < ?)`, f.name, f.id)
			if err != nil {
				return err
			}
			if taken {
				firstNumber = 2
			}
		}

		finalKey := f.key
		if firstNumber > 0 {
			var finalName string
			for n := firstNumber; ; n++ {
				finalName = numberedTeamName20261117141344(f.name, n)
				finalKey = teamNameKey20261117141344(finalName)
				if liveKeys[finalKey] > 0 {
					continue
				}
				taken, err := nameTaken(`SELECT EXISTS (SELECT 1 FROM teams WHERE name = ? AND id != ?)`, finalName, f.id)
				if err != nil {
					return err
				}
				if !taken {
					break
				}
			}
			if _, err := tx.Exec(`UPDATE teams SET name = ? WHERE id = ?`, finalName, f.id); err != nil {
				return fmt.Errorf("rename team %d: %w", f.id, err)
			}
			logger.Info.Printf("Renamed team ID %d from %q to %q to follow the AutoMDM 5 team name rules.\n", f.id, f.name, finalName)
			renamed = append(renamed, renamedTeam20261117141344{id: f.id, oldName: f.name, newName: finalName})
		}
		finalKeys[finalKey] = struct{}{}
		liveKeys[finalKey]++
	}

	if len(renamed) == 0 {
		return nil
	}
	return rewriteTeamNameCopies20261117141344(tx, renamed)
}

// forEachRow20261117141344 closes the rows before returning, so the transaction's connection is free for the next query.
func forEachRow20261117141344(tx *sql.Tx, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// numberedTeamName20261117141344 appends " n" to name, shortening name so the result fits the teams.name column.
func numberedTeamName20261117141344(name string, n int) string {
	suffix := " " + strconv.Itoa(n)
	if runes := []rune(name); len(runes)+len(suffix) > teamNameMaxLength20261117141344 {
		name = string(runes[:teamNameMaxLength20261117141344-len(suffix)])
	}
	return name + suffix
}

// rewriteTeamNameCopies20261117141344 updates the copies of the renamed teams' names that app config keeps for
// Apple Business and VPP tokens, the same copies a rename through the API updates. abm_tokens and vpp_token_teams are
// the source of truth, so only the entries of tokens assigned to a renamed team are touched. Keys are matched in both
// their AutoMDM 4 and AutoMDM 5 spellings.
func rewriteTeamNameCopies20261117141344(tx *sql.Tx, renamed []renamedTeam20261117141344) error {
	var raw []byte
	switch err := tx.QueryRow(`SELECT json_value FROM app_config_json WHERE id = 1`).Scan(&raw); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("select app config: %w", err)
	}
	var appConfig struct {
		MDM map[string]any `json:"mdm"`
	}
	if err := json.Unmarshal(raw, &appConfig); err != nil {
		return fmt.Errorf("unmarshal app config: %w", err)
	}

	// App config holds what the admin typed, which can be in a different Unicode normalization form than the team's
	// name or the token's organization name or location. The server matches all three after NFC normalization.
	sameName := func(copied any, name string) bool {
		s, ok := copied.(string)
		return ok && s != "" && norm.NFC.String(s) == norm.NFC.String(name)
	}

	type nameCopy struct{ path, newName string }
	var copies []nameCopy
	set := func(path, newName string) {
		copies = append(copies, nameCopy{path: path, newName: newName})
	}

	abmTeamIDs := make(map[string]map[uint]struct{}) // organization name to default team IDs
	err := forEachRow20261117141344(tx, `
		SELECT organization_name, macos_default_team_id, ios_default_team_id, ipados_default_team_id, byod_default_team_id
		FROM abm_tokens`,
		func(rows *sql.Rows) error {
			var orgName string
			var ids [4]sql.Null[uint]
			if err := rows.Scan(&orgName, &ids[0], &ids[1], &ids[2], &ids[3]); err != nil {
				return err
			}
			orgName = norm.NFC.String(orgName)
			abmTeamIDs[orgName] = make(map[uint]struct{})
			for _, id := range ids {
				if id.Valid {
					abmTeamIDs[orgName][id.V] = struct{}{}
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("select Apple Business tokens: %w", err)
	}
	abmNameKeys := []string{
		"macos_team", "ios_team", "ipados_team", "byod_team",
		"macos_team", "ios_team", "ipados_team", "byod_team",
	}
	for _, listKey := range []string{"apple_business_manager", "apple_business"} {
		entries, _ := appConfig.MDM[listKey].([]any)
		for i, e := range entries {
			entry, _ := e.(map[string]any)
			orgName, _ := entry["organization_name"].(string)
			orgName = norm.NFC.String(orgName)
			for _, nameKey := range abmNameKeys {
				for _, r := range renamed {
					if _, ok := abmTeamIDs[orgName][r.id]; ok && sameName(entry[nameKey], r.oldName) {
						set(fmt.Sprintf("$.mdm.%s[%d].%s", listKey, i, nameKey), r.newName)
						break
					}
				}
			}
		}
	}

	vppAutoMDMIDs := make(map[string]map[uint]struct{}) // location to assigned team IDs
	err = forEachRow20261117141344(tx, `
		SELECT vt.location, vtt.team_id
		FROM vpp_token_teams vtt
		JOIN vpp_tokens vt ON vt.id = vtt.vpp_token_id
		WHERE vtt.team_id IS NOT NULL`,
		func(rows *sql.Rows) error {
			var location string
			var id uint
			if err := rows.Scan(&location, &id); err != nil {
				return err
			}
			location = norm.NFC.String(location)
			if vppAutoMDMIDs[location] == nil {
				vppAutoMDMIDs[location] = make(map[uint]struct{})
			}
			vppAutoMDMIDs[location][id] = struct{}{}
			return nil
		})
	if err != nil {
		return fmt.Errorf("select VPP token teams: %w", err)
	}
	entries, _ := appConfig.MDM["volume_purchasing_program"].([]any)
	for i, e := range entries {
		entry, _ := e.(map[string]any)
		location, _ := entry["location"].(string)
		location = norm.NFC.String(location)
		for _, listKey := range []string{"teams", "teams"} {
			names, _ := entry[listKey].([]any)
			for j, name := range names {
				for _, r := range renamed {
					if _, ok := vppAutoMDMIDs[location][r.id]; ok && sameName(name, r.oldName) {
						set(fmt.Sprintf("$.mdm.volume_purchasing_program[%d].%s[%d]", i, listKey, j), r.newName)
						break
					}
				}
			}
		}
	}

	// Deprecated, and not tied to a token.
	for _, r := range renamed {
		if sameName(appConfig.MDM["apple_bm_default_team"], r.oldName) {
			set("$.mdm.apple_bm_default_team", r.newName)
			break
		}
	}

	for _, c := range copies {
		if _, err := tx.Exec(`UPDATE app_config_json SET json_value = JSON_SET(json_value, ?, ?) WHERE id = 1`, c.path, c.newName); err != nil {
			return fmt.Errorf("update team name at %s in app config: %w", c.path, err)
		}
	}
	return nil
}

func Down_20261117141344(tx *sql.Tx) error {
	return nil
}
