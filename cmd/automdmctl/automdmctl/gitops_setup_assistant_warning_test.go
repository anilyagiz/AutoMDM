package automdmctl

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/assert"
)

type fakeABMChecker struct {
	tokens       []*automdm.ABMToken
	tokensErr    error
	hostCounts   map[string]int // raw query -> count
	countErr     error
	countQueries []string
}

func (f *fakeABMChecker) ListABTokens() ([]*automdm.ABMToken, error) { return f.tokens, f.tokensErr }
func (f *fakeABMChecker) CountHosts(query string) (int, error) {
	f.countQueries = append(f.countQueries, query)
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.hostCounts[query], nil
}

func abmToken(macos, ios, ipados, byod string) *automdm.ABMToken {
	return &automdm.ABMToken{
		MacOSTeam:  automdm.ABMTokenTeam{Name: macos},
		IOSTeam:    automdm.ABMTokenTeam{Name: ios},
		IPadOSTeam: automdm.ABMTokenTeam{Name: ipados},
		BYODTeam:   automdm.ABMTokenTeam{Name: byod},
	}
}

func TestWarnSetupAssistantsWithoutABMTokens(t *testing.T) {
	const warnFragment = "won't take effect"

	cases := []struct {
		name         string
		checker      *fakeABMChecker
		teams        map[string]uint
		wantWarnedOn []string
	}{
		{
			name: "team is a token default on any platform",
			checker: &fakeABMChecker{
				tokens: []*automdm.ABMToken{abmToken("Workstations", "Mobile", "Mobile", "No team")},
			},
			teams:        map[string]uint{"Workstations": 1, "Mobile": 2},
			wantWarnedOn: nil,
		},
		{
			name: "team not covered and no ABM hosts",
			checker: &fakeABMChecker{
				tokens: []*automdm.ABMToken{abmToken("Workstations", "", "", "")},
			},
			teams:        map[string]uint{"Lab": 7},
			wantWarnedOn: []string{"Lab"},
		},
		{
			name: "team not a default but has ABM hosts",
			checker: &fakeABMChecker{
				tokens: []*automdm.ABMToken{abmToken("Workstations", "", "", "")},
				hostCounts: map[string]int{
					fmt.Sprintf("team_id=7&mdm_enrollment_status=%s", automdm.MDMEnrollStatusPending): 2,
				},
			},
			teams:        map[string]uint{"Lab": 7},
			wantWarnedOn: nil,
		},
		{
			name: "no team covered by token without defaults",
			checker: &fakeABMChecker{
				// ListABMTokens reports Unassigned for unset platform defaults.
				tokens: []*automdm.ABMToken{abmToken("Workstations", automdm.DisplayNameNoTeam, automdm.DisplayNameNoTeam, automdm.DisplayNameNoTeam)},
			},
			teams:        map[string]uint{automdm.DisplayNameNoTeam: 0},
			wantWarnedOn: nil,
		},
		{
			name: "no team not covered and no ABM hosts",
			checker: &fakeABMChecker{
				tokens: []*automdm.ABMToken{abmToken("Workstations", "Workstations", "Workstations", "Workstations")},
			},
			teams:        map[string]uint{automdm.DisplayNameNoTeam: 0},
			wantWarnedOn: []string{automdm.DisplayNameNoTeam},
		},
		{
			name:         "token listing error stays silent about coverage",
			checker:      &fakeABMChecker{tokensErr: errors.New("boom")},
			teams:        map[string]uint{"Lab": 7},
			wantWarnedOn: nil,
		},
		{
			name: "host count error suppresses the warning rather than risking a false one",
			checker: &fakeABMChecker{
				tokens:   []*automdm.ABMToken{abmToken("Workstations", "", "", "")},
				countErr: errors.New("boom"),
			},
			teams:        map[string]uint{"Lab": 7},
			wantWarnedOn: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			logf := func(format string, a ...any) { fmt.Fprintf(&out, format, a...) }

			warnSetupAssistantsWithoutABMTokens(c.checker, c.teams, logf)

			for _, name := range c.wantWarnedOn {
				assert.Contains(t, out.String(), fmt.Sprintf("team %s: setup assistant saved", name))
				assert.Contains(t, out.String(), warnFragment)
			}
			if len(c.wantWarnedOn) == 0 {
				assert.NotContains(t, out.String(), warnFragment)
			}
		})
	}
}
