package service

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/pkg/optjson"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/require"
)

func TestCheckPolicyAutomations(t *testing.T) {
	const (
		script   = automdm.PolicyAutomationScript
		calendar = automdm.PolicyAutomationCalendar
		webhook  = automdm.PolicyAutomationWebhookOrTicket
	)
	team, noTeam := new(uint(1)), new(uint(0))
	for _, c := range []struct {
		name          string
		teamID        *uint
		before, after []automdm.PolicyAutomation
		wantErr       string
	}{
		{"one automation", team, nil, []automdm.PolicyAutomation{script}, ""},
		{"two automations", team, nil, []automdm.PolicyAutomation{script, calendar}, errPolicyOneAutomation},
		{"adding a second", team, []automdm.PolicyAutomation{script}, []automdm.PolicyAutomation{script, webhook}, errPolicyOneAutomation},
		{"saved with two before the rule", team, []automdm.PolicyAutomation{script, webhook}, []automdm.PolicyAutomation{script, webhook}, ""},
		{"saved with two, one removed", team, []automdm.PolicyAutomation{script, webhook}, []automdm.PolicyAutomation{script}, ""},
		{"saved with two, one swapped", team, []automdm.PolicyAutomation{script, webhook}, []automdm.PolicyAutomation{script, calendar}, errPolicyOneAutomation},
		{"All teams webhook", nil, nil, []automdm.PolicyAutomation{webhook}, ""},
		{"All teams calendar", nil, nil, []automdm.PolicyAutomation{calendar}, "can only have automation_type"},
		{"All teams calendar saved before the rule", nil, []automdm.PolicyAutomation{calendar}, []automdm.PolicyAutomation{calendar}, ""},
		{"Unassigned script", noTeam, nil, []automdm.PolicyAutomation{script}, ""},
		{"Unassigned calendar", noTeam, nil, []automdm.PolicyAutomation{calendar}, `can't have automation_type "calendar"`},
		{"team calendar", team, nil, []automdm.PolicyAutomation{calendar}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := checkPolicyAutomations(c.teamID, c.before, c.after)
			if c.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.wantErr)
		})
	}
}

func TestApplyAutomationType(t *testing.T) {
	team := new(uint(1))
	automation := func(a automdm.PolicyAutomation) optjson.Any[automdm.PolicyAutomation] {
		return optjson.Any[automdm.PolicyAutomation]{Set: true, Valid: true, Value: a}
	}

	// Choosing an automation clears the others.
	p := automdm.ModifyPolicyPayload{
		AutomationType: automation(automdm.PolicyAutomationScript),
		ScriptID:       optjson.Any[uint]{Set: true, Valid: true, Value: 5},
	}
	webhook, err := applyAutomationType(team, &p)
	require.NoError(t, err)
	require.False(t, webhook)
	require.Equal(t, uint(5), p.ScriptID.Value)
	require.True(t, p.SoftwareTitleID.Set)
	require.False(t, p.SoftwareTitleID.Valid)
	require.True(t, p.ProfileUUID.Set)
	require.False(t, *p.CalendarEventsEnabled)
	require.False(t, *p.ConditionalAccessEnabled)

	p = automdm.ModifyPolicyPayload{AutomationType: automation(automdm.PolicyAutomationWebhookOrTicket), CalendarEventsEnabled: new(false)}
	webhook, err = applyAutomationType(team, &p)
	require.NoError(t, err)
	require.True(t, webhook)
	require.True(t, p.ScriptID.Set)
	require.False(t, p.ScriptID.Valid)

	p = automdm.ModifyPolicyPayload{AutomationType: automation(automdm.PolicyAutomationCalendar)}
	_, err = applyAutomationType(team, &p)
	require.NoError(t, err)
	require.True(t, *p.CalendarEventsEnabled)

	// null removes the automation, and doesn't take a companion field.
	p = automdm.ModifyPolicyPayload{AutomationType: optjson.Any[automdm.PolicyAutomation]{Set: true}}
	webhook, err = applyAutomationType(team, &p)
	require.NoError(t, err)
	require.False(t, webhook)
	p = automdm.ModifyPolicyPayload{
		AutomationType: optjson.Any[automdm.PolicyAutomation]{Set: true},
		ScriptID:       optjson.Any[uint]{Set: true, Valid: true, Value: 5},
	}
	_, err = applyAutomationType(team, &p)
	require.ErrorContains(t, err, "can't be set when automation_type is null")

	// A legacy field that turns on another automation is a second automation.
	p = automdm.ModifyPolicyPayload{AutomationType: automation(automdm.PolicyAutomationWebhookOrTicket), ConditionalAccessEnabled: new(true)}
	_, err = applyAutomationType(team, &p)
	require.ErrorContains(t, err, errPolicyOneAutomation)

	// Companion fields have to match, and the scope rules apply.
	p = automdm.ModifyPolicyPayload{
		AutomationType: automation(automdm.PolicyAutomationSoftware),
		ProfileUUID:    optjson.String{Set: true, Valid: true, Value: "a"},
	}
	_, err = applyAutomationType(team, &p)
	require.ErrorContains(t, err, `automation_type "software" requires software_title_id`)
	p = automdm.ModifyPolicyPayload{AutomationType: automation(automdm.PolicyAutomationScript)}
	_, err = applyAutomationType(nil, &p)
	require.ErrorContains(t, err, `can only have automation_type "webhook_or_ticket"`)
	p = automdm.ModifyPolicyPayload{AutomationType: automation("other")}
	_, err = applyAutomationType(team, &p)
	require.ErrorContains(t, err, `invalid automation_type "other"`)
}
