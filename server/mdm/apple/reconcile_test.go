package apple_mdm

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/config"
	"github.com/anilyagiz/automdm/v4/server/contexts/license"
	"github.com/anilyagiz/automdm/v4/server/mdm/apple/mobileconfig"
	"github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/mdm"
	nanomdm_pushsvc "github.com/anilyagiz/automdm/v4/server/mdm/nanomdm/push/service"
	"github.com/anilyagiz/automdm/v4/server/mock"
	mdmmock "github.com/anilyagiz/automdm/v4/server/mock/mdm"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	micromdm "github.com/micromdm/micromdm/mdm/mdm"
	"github.com/micromdm/nanolib/log/stdlogfmt"
	"github.com/micromdm/plist"
	"github.com/smallstep/pkcs7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The include/exclude label handlers and the team + include/exclude logic of the dispatcher are platform-neutral and live in
// server/mdm/reconcile, which owns their exhaustive unit tests. The Apple tests below cover only what is Apple-specific: the
// platform gate, the threading of host fields (EffectiveTeamID, LabelUpdatedAt) through the Apple wrapper into the shared
// dispatcher, the declarations-share-the-dispatcher drift contract, and the Apple compute/execute logic.

func TestEntityAppliesToHost_AppleWrapper(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID:   1,
		UUID:     "h1",
		TeamID:   nil,
		Platform: "darwin",
	}

	t.Run("wrong team -> false", func(t *testing.T) {
		p := &automdm.AppleProfileForReconcile{TeamID: 5, IncludeMode: automdm.AppleProfileIncludeNone}
		require.False(t, EntityAppliesToHost(p, host, nil, false))
	})
	t.Run("global team matches nil team_id host", func(t *testing.T) {
		p := &automdm.AppleProfileForReconcile{TeamID: 0, IncludeMode: automdm.AppleProfileIncludeNone}
		require.True(t, EntityAppliesToHost(p, host, nil, false))
	})
	t.Run("non-apple platform -> false (Apple-only platform gate)", func(t *testing.T) {
		linuxHost := *host
		linuxHost.Platform = "linux"
		p := &automdm.AppleProfileForReconcile{TeamID: 0, IncludeMode: automdm.AppleProfileIncludeNone}
		require.False(t, EntityAppliesToHost(p, &linuxHost, nil, false))
	})
	// The shared package tests the dynamic-label unknown-membership rule itself;
	// this case pins that the Apple wrapper threads host.LabelUpdatedAt and the
	// on-host state into it, rather than e.g. a zero time.
	t.Run("host.LabelUpdatedAt is threaded into the exclude unknown-membership gate", func(t *testing.T) {
		dynamicExcLabel := automdm.AppleProfileLabelRef{
			LabelID:             new(uint(99)),
			LabelMembershipType: int(automdm.LabelMembershipTypeDynamic),
			CreatedAt:           time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		}
		p := &automdm.AppleProfileForReconcile{
			TeamID:        0,
			IncludeMode:   automdm.AppleProfileIncludeAny,
			IncludeLabels: []automdm.AppleProfileLabelRef{{LabelID: new(uint(1))}},
			ExcludeLabels: []automdm.AppleProfileLabelRef{dynamicExcLabel},
		}
		// Host scanned before the dynamic exclude label was created: membership is
		// unknown, so the current state is preserved — withheld when not on the
		// host, kept when already on it.
		staleHost := &automdm.AppleHostReconcileInfo{
			HostID: 1, UUID: "h1", TeamID: nil, Platform: "darwin",
			LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
		require.False(t, EntityAppliesToHost(p, staleHost, map[uint]struct{}{1: {}}, false))
		require.True(t, EntityAppliesToHost(p, staleHost, map[uint]struct{}{1: {}}, true))
		// Once the host's scan advances past the label's CreatedAt, it applies.
		freshHost := &automdm.AppleHostReconcileInfo{
			HostID: 1, UUID: "h1", TeamID: nil, Platform: "darwin",
			LabelUpdatedAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		}
		require.True(t, EntityAppliesToHost(p, freshHost, map[uint]struct{}{1: {}}, false))
	})
}

// TestEntityAppliesToHost_DeclarationsShareSameDispatcher pins the
// drift-prevention contract: feeding the same label config to both a
// *AppleProfileForReconcile and a *AppleDeclarationForReconcile must
// produce the same applies-to-host result. If this test breaks, the
// AppleLabeledEntity interface is no longer the single source of truth.
func TestEntityAppliesToHost_DeclarationsShareSameDispatcher(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "h1", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}

	commonLabels := []automdm.AppleProfileLabelRef{
		{LabelID: new(uint(1))},
		{LabelID: new(uint(2))},
	}
	commonExclude := []automdm.AppleProfileLabelRef{
		{LabelID: new(uint(9)), CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	}

	prof := &automdm.AppleProfileForReconcile{
		ProfileUUID: "aProf", TeamID: 0,
		IncludeMode:   automdm.AppleProfileIncludeAll,
		IncludeLabels: commonLabels,
		ExcludeLabels: commonExclude,
	}
	decl := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID: "aDecl", TeamID: 0,
		IncludeMode:   automdm.AppleProfileIncludeAll,
		IncludeLabels: commonLabels,
		ExcludeLabels: commonExclude,
	}

	cases := []struct {
		name       string
		hostLabels map[uint]struct{}
		want       bool
	}{
		{"both required + not in exclude", map[uint]struct{}{1: {}, 2: {}}, true},
		{"missing one required", map[uint]struct{}{1: {}}, false},
		{"in exclude", map[uint]struct{}{1: {}, 2: {}, 9: {}}, false},
		{"neither included", map[uint]struct{}{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr := EntityAppliesToHost(prof, host, c.hostLabels, false)
			dr := EntityAppliesToHost(decl, host, c.hostLabels, false)
			require.Equal(t, c.want, pr, "profile result")
			require.Equal(t, c.want, dr, "declaration result")
			require.Equal(t, pr, dr,
				"PROFILE/DECLARATION DRIFT: same label config produced different results — AppleLabeledEntity contract broken")
		})
	}
}

func TestComputeReconcileDeltas(t *testing.T) {
	hostA := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	hostB := &automdm.AppleHostReconcileInfo{
		HostID: 2, UUID: "uuid-B", TeamID: new(uint(7)), Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}

	pGlobal := &automdm.AppleProfileForReconcile{
		ProfileUUID:       "aProfileGlobal",
		ProfileIdentifier: "com.example.global",
		ProfileName:       "Global",
		TeamID:            0,
		Checksum:          []byte("aaaa"),
		IncludeMode:       automdm.AppleProfileIncludeNone,
	}
	pTeam7 := &automdm.AppleProfileForReconcile{
		ProfileUUID:       "aProfileTeam7",
		ProfileIdentifier: "com.example.team7",
		ProfileName:       "Team7",
		TeamID:            7,
		Checksum:          []byte("bbbb"),
		IncludeMode:       automdm.AppleProfileIncludeNone,
	}
	profilesByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pGlobal}, 7: {pTeam7}}
	profilesWithBrokenLabel := map[string]struct{}{}

	t.Run("desired but not present -> install", func(t *testing.T) {
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA, hostB}, nil, nil, profilesByTeam, profilesWithBrokenLabel, nil,
		)
		require.Empty(t, toRemove)
		require.Len(t, toInstall, 2)
		set := map[string]string{}
		for _, p := range toInstall {
			set[p.HostUUID] = p.ProfileUUID
		}
		require.Equal(t, "aProfileGlobal", set["uuid-A"])
		require.Equal(t, "aProfileTeam7", set["uuid-B"])
	})

	t.Run("checksum differs -> install", func(t *testing.T) {
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aProfileGlobal",
				HostUUID:      "uuid-A",
				Checksum:      []byte("OLD!"),
				OperationType: automdm.MDMOperationTypeInstall,
				Status:        new(automdm.MDMDeliveryVerified),
			}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, profilesByTeam, profilesWithBrokenLabel, nil,
		)
		require.Empty(t, toRemove)
		require.Len(t, toInstall, 1)
		require.True(t, bytes.Equal(toInstall[0].Checksum, []byte("aaaa")))
	})

	t.Run("not in desired -> remove (when not broken-label)", func(t *testing.T) {
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:       "aDeletedProfile",
				ProfileIdentifier: "com.deleted",
				HostUUID:          "uuid-A",
				Checksum:          []byte("xxxx"),
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            new(automdm.MDMDeliveryVerified),
			}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, profilesByTeam, profilesWithBrokenLabel, nil,
		)
		require.Len(t, toInstall, 1)
		require.Len(t, toRemove, 1)
		require.Equal(t, "aDeletedProfile", toRemove[0].ProfileUUID)
	})

	t.Run("combined include_all + exclude_any: host in include but also in exclude -> not desired", func(t *testing.T) {
		pCombined := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aCombined",
			ProfileIdentifier: "com.example.combined",
			ProfileName:       "Combined",
			TeamID:            0,
			Checksum:          []byte("cccc"),
			IncludeMode:       automdm.AppleProfileIncludeAll,
			IncludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(10))}},
			ExcludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(20)), CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pGlobal, pCombined}}

		// Host in include label but also in exclude label -> combined profile not desired
		hostLabels := map[uint]map[uint]struct{}{
			hostA.HostID: {10: {}, 20: {}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, hostLabels, nil, profByTeam, profilesWithBrokenLabel, nil,
		)
		require.Empty(t, toRemove)
		require.Len(t, toInstall, 1)
		require.Equal(t, "aProfileGlobal", toInstall[0].ProfileUUID)
	})

	t.Run("combined include_all + exclude_any: host in include not in exclude -> desired", func(t *testing.T) {
		pCombined := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aCombined",
			ProfileIdentifier: "com.example.combined",
			ProfileName:       "Combined",
			TeamID:            0,
			Checksum:          []byte("cccc"),
			IncludeMode:       automdm.AppleProfileIncludeAll,
			IncludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(10))}},
			ExcludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(20)), CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pGlobal, pCombined}}

		// Host in include label, not in exclude -> both profiles desired
		hostLabels := map[uint]map[uint]struct{}{
			hostA.HostID: {10: {}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, hostLabels, nil, profByTeam, profilesWithBrokenLabel, nil,
		)
		require.Empty(t, toRemove)
		require.Len(t, toInstall, 2)
	})

	t.Run("combined include_any + exclude_any: host in include but also in exclude -> not desired, removed if present", func(t *testing.T) {
		pCombined := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aCombined",
			ProfileIdentifier: "com.example.combined",
			ProfileName:       "Combined",
			TeamID:            0,
			Checksum:          []byte("cccc"),
			IncludeMode:       automdm.AppleProfileIncludeAny,
			IncludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(10))}, {LabelID: new(uint(11))}},
			ExcludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(20)), CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pGlobal, pCombined}}

		// Host in one include label AND in exclude label -> combined profile no longer desired, should be removed
		hostLabels := map[uint]map[uint]struct{}{
			hostA.HostID: {10: {}, 20: {}},
		}
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aCombined",
				HostUUID:      "uuid-A",
				Checksum:      []byte("cccc"),
				OperationType: automdm.MDMOperationTypeInstall,
				Status:        new(automdm.MDMDeliveryVerified),
			}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, hostLabels, current, profByTeam, profilesWithBrokenLabel, nil,
		)
		require.Len(t, toInstall, 1) // pGlobal
		require.Len(t, toRemove, 1)
		require.Equal(t, "aCombined", toRemove[0].ProfileUUID)
	})

	t.Run("broken label profile is not removed", func(t *testing.T) {
		brokenProf := &automdm.AppleProfileForReconcile{
			ProfileUUID:   "aBrokenLabel",
			TeamID:        0,
			IncludeMode:   automdm.AppleProfileIncludeAll,
			IncludeLabels: []automdm.AppleProfileLabelRef{{LabelID: nil}},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pGlobal, brokenProf}}
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aBrokenLabel",
				HostUUID:      "uuid-A",
				Checksum:      []byte("xxxx"),
				OperationType: automdm.MDMOperationTypeInstall,
				Status:        new(automdm.MDMDeliveryVerified),
			}},
		}
		profilesWithBrokenLabel := map[string]struct{}{"aBrokenLabel": {}}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, profByTeam, profilesWithBrokenLabel, nil,
		)
		require.Len(t, toInstall, 1) // pGlobal still installs
		require.Empty(t, toRemove)
	})

	// Unknown-membership preservation: a dynamic label created after the host's
	// last label scan must preserve the host's current profile state instead of
	// forcing a removal (see #47865).
	t.Run("unknown dynamic exclude label preserves current state", func(t *testing.T) {
		unknownExc := automdm.AppleProfileLabelRef{
			LabelID:             new(uint(30)),
			LabelMembershipType: int(automdm.LabelMembershipTypeDynamic),
			CreatedAt:           hostA.LabelUpdatedAt.Add(time.Hour),
		}
		pExc := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aExcProf",
			ProfileIdentifier: "com.example.exc",
			ProfileName:       "Exc",
			TeamID:            0,
			Checksum:          []byte("eeee"),
			IncludeMode:       automdm.AppleProfileIncludeNone,
			ExcludeLabels:     []automdm.AppleProfileLabelRef{unknownExc},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pExc}}

		// Not on the host: withheld until the host reports label results.
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, nil, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)

		// Already installed on the host: kept, no removal.
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aExcProf",
				HostUUID:      "uuid-A",
				Checksum:      []byte("eeee"),
				OperationType: automdm.MDMOperationTypeInstall,
				Status:        new(automdm.MDMDeliveryVerified),
			}},
		}
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)

		// Host reports label results (scan time advances past the label's
		// CreatedAt): membership is now authoritative. Member -> removed.
		freshHost := *hostA
		freshHost.LabelUpdatedAt = unknownExc.CreatedAt.Add(time.Hour)
		hostLabels := map[uint]map[uint]struct{}{hostA.HostID: {30: {}}}
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, hostLabels, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Len(t, toRemove, 1)
		require.Equal(t, "aExcProf", toRemove[0].ProfileUUID)

		// Non-member after the scan -> kept installed, and installed on hosts
		// that didn't have it.
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, nil, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, nil, nil, profByTeam, map[string]struct{}{}, nil,
		)
		require.Len(t, toInstall, 1)
		require.Empty(t, toRemove)
	})

	t.Run("unknown dynamic include_all label preserves current state", func(t *testing.T) {
		unknownInc := automdm.AppleProfileLabelRef{
			LabelID:             new(uint(31)),
			LabelMembershipType: int(automdm.LabelMembershipTypeDynamic),
			CreatedAt:           hostA.LabelUpdatedAt.Add(time.Hour),
		}
		pInc := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aIncProf",
			ProfileIdentifier: "com.example.inc",
			ProfileName:       "Inc",
			TeamID:            0,
			Checksum:          []byte("ffff"),
			IncludeMode:       automdm.AppleProfileIncludeAll,
			IncludeLabels:     []automdm.AppleProfileLabelRef{{LabelID: new(uint(10))}, unknownInc},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pInc}}
		// Host is a confirmed member of the pre-existing include label only.
		hostLabels := map[uint]map[uint]struct{}{hostA.HostID: {10: {}}}

		// Not on the host: withheld until membership in the new label is known.
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, hostLabels, nil, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)

		// Already installed on the host: kept, no removal.
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aIncProf",
				HostUUID:      "uuid-A",
				Checksum:      []byte("ffff"),
				OperationType: automdm.MDMOperationTypeInstall,
				Status:        new(automdm.MDMDeliveryVerified),
			}},
		}
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, hostLabels, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)

		// Scan advances, host confirmed NOT a member of the new label -> removed.
		freshHost := *hostA
		freshHost.LabelUpdatedAt = unknownInc.CreatedAt.Add(time.Hour)
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, hostLabels, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Len(t, toRemove, 1)
		require.Equal(t, "aIncProf", toRemove[0].ProfileUUID)

		// Scan advances, host confirmed a member of both labels -> stays.
		memberLabels := map[uint]map[uint]struct{}{hostA.HostID: {10: {}, 31: {}}}
		toInstall, toRemove, _ = ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, memberLabels, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)
	})

	t.Run("unknown labels do not preserve a profile pending removal", func(t *testing.T) {
		unknownExc := automdm.AppleProfileLabelRef{
			LabelID:             new(uint(32)),
			LabelMembershipType: int(automdm.LabelMembershipTypeDynamic),
			CreatedAt:           hostA.LabelUpdatedAt.Add(time.Hour),
		}
		pExc := &automdm.AppleProfileForReconcile{
			ProfileUUID:       "aExcProf",
			ProfileIdentifier: "com.example.exc",
			ProfileName:       "Exc",
			TeamID:            0,
			Checksum:          []byte("eeee"),
			IncludeMode:       automdm.AppleProfileIncludeNone,
			ExcludeLabels:     []automdm.AppleProfileLabelRef{unknownExc},
		}
		profByTeam := map[uint][]*automdm.AppleProfileForReconcile{0: {pExc}}
		// A remove operation is in flight: the profile is NOT considered on the
		// host, so the unknown exclude label keeps it withheld (no flip back to
		// install) and the in-flight removal proceeds untouched.
		current := map[string][]*automdm.MDMAppleProfilePayload{
			"uuid-A": {{
				ProfileUUID:   "aExcProf",
				HostUUID:      "uuid-A",
				Checksum:      []byte("eeee"),
				OperationType: automdm.MDMOperationTypeRemove,
				Status:        new(automdm.MDMDeliveryPending),
			}},
		}
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, profByTeam, map[string]struct{}{}, nil,
		)
		require.Empty(t, toInstall)
		require.Empty(t, toRemove)
	})
}

func TestComputeDeclarationDeltas(t *testing.T) {
	hostA := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	dGlobal := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID:       "aDeclGlobal",
		DeclarationIdentifier: "com.example.decl.global",
		DeclarationName:       "GlobalDecl",
		TeamID:                0,
		Token:                 []byte("tok1"),
		IncludeMode:           automdm.AppleProfileIncludeNone,
	}
	declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {dGlobal}}
	declsWithBrokenLabel := map[string]struct{}{}

	t.Run("desired but not present -> install diff", func(t *testing.T) {
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, nil, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Empty(t, changedUser)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.MDMOperationTypeInstall, rows[0].OperationType)
		require.Equal(t, "aDeclGlobal", rows[0].DeclarationUUID)
	})

	t.Run("token matches and op=install,status=pending -> no diff", func(t *testing.T) {
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclGlobal",
				Token:           "tok1",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryPending),
			}},
		}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)
	})

	t.Run("token differs -> install diff", func(t *testing.T) {
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclGlobal",
				Token:           "OLD!",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryVerified),
			}},
		}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Empty(t, changedUser)
		require.Len(t, rows, 1)
		require.Equal(t, "tok1", rows[0].Token)
	})

	t.Run("unknown dynamic exclude label preserves current declaration state", func(t *testing.T) {
		unknownExc := automdm.AppleProfileLabelRef{
			LabelID:             new(uint(40)),
			LabelMembershipType: int(automdm.LabelMembershipTypeDynamic),
			CreatedAt:           hostA.LabelUpdatedAt.Add(time.Hour),
		}
		dExc := &automdm.AppleDeclarationForReconcile{
			DeclarationUUID:       "aDeclExc",
			DeclarationIdentifier: "com.example.decl.exc",
			DeclarationName:       "ExcDecl",
			TeamID:                0,
			Token:                 []byte("tokE"),
			IncludeMode:           automdm.AppleProfileIncludeNone,
			ExcludeLabels:         []automdm.AppleProfileLabelRef{unknownExc},
		}
		byTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {dExc}}

		// Not on the host: withheld, nothing changes.
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, nil, byTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)

		// Already installed on the host: kept, no removal row.
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclExc",
				Token:           "tokE",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryVerified),
			}},
		}
		changedDevice, changedUser, rows = ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{hostA}, nil, current, byTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)

		// Scan advances and the host is a confirmed member -> removal row.
		freshHost := *hostA
		freshHost.LabelUpdatedAt = unknownExc.CreatedAt.Add(time.Hour)
		hostLabels := map[uint]map[uint]struct{}{hostA.HostID: {40: {}}}
		changedDevice, changedUser, rows = ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{&freshHost}, hostLabels, current, byTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Empty(t, changedUser)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.MDMOperationTypeRemove, rows[0].OperationType)
	})
}

func TestComputeDeclarationDeltasScope(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	deviceDecl := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID: "aDeviceDecl", DeclarationIdentifier: "com.example.device", DeclarationName: "Device",
		TeamID: 0, Token: []byte("tokD"), Scope: automdm.PayloadScopeSystem, IncludeMode: automdm.AppleProfileIncludeNone,
	}
	userDecl := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID: "aUserDecl", DeclarationIdentifier: "com.example.user", DeclarationName: "User",
		TeamID: 0, Token: []byte("tokU"), Scope: automdm.PayloadScopeUser, IncludeMode: automdm.AppleProfileIncludeNone,
	}
	declsWithBrokenLabel := map[string]struct{}{}

	scopeByUUID := func(rows []*automdm.MDMAppleHostDeclaration) map[string]automdm.PayloadScope {
		m := make(map[string]automdm.PayloadScope, len(rows))
		for _, r := range rows {
			m[r.DeclarationUUID] = r.Scope
		}
		return m
	}

	t.Run("user-scoped install pokes only the user channel", func(t *testing.T) {
		declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {userDecl}}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, nil, declsByTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.ElementsMatch(t, []string{"uuid-A"}, changedUser)
		require.Equal(t, automdm.PayloadScopeUser, scopeByUUID(rows)["aUserDecl"])
	})

	t.Run("device- and user-scoped changes poke both channels", func(t *testing.T) {
		declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {deviceDecl, userDecl}}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, nil, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.ElementsMatch(t, []string{"uuid-A"}, changedUser)
		byUUID := scopeByUUID(rows)
		require.Equal(t, automdm.PayloadScopeSystem, byUUID["aDeviceDecl"])
		require.Equal(t, automdm.PayloadScopeUser, byUUID["aUserDecl"])
	})

	t.Run("scope flip pokes both the old and new channel", func(t *testing.T) {
		// The declaration is currently installed as System on the host but is now
		// desired as User: the new (user) channel installs it and the old (device)
		// channel must drop it, so both channels are poked.
		flipped := &automdm.AppleDeclarationForReconcile{
			DeclarationUUID: "aFlip", DeclarationIdentifier: "com.example.flip", DeclarationName: "Flip",
			TeamID: 0, Token: []byte("tokF"), Scope: automdm.PayloadScopeUser, IncludeMode: automdm.AppleProfileIncludeNone,
		}
		declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {flipped}}
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID: "uuid-A", DeclarationUUID: "aFlip", Token: "tokF",
				OperationType: automdm.MDMOperationTypeInstall, Status: new(automdm.MDMDeliveryVerified),
				Scope: automdm.PayloadScopeSystem,
			}},
		}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.ElementsMatch(t, []string{"uuid-A"}, changedUser)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.PayloadScopeUser, rows[0].Scope)
		require.Equal(t, automdm.MDMOperationTypeInstall, rows[0].OperationType)
	})

	t.Run("user-scoped removal pokes the user channel", func(t *testing.T) {
		// No longer desired; currently installed on the user channel.
		declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {}}
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID: "uuid-A", DeclarationUUID: "aUserDecl", Token: "tokU",
				OperationType: automdm.MDMOperationTypeInstall, Status: new(automdm.MDMDeliveryVerified),
				Scope: automdm.PayloadScopeUser,
			}},
		}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.ElementsMatch(t, []string{"uuid-A"}, changedUser)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.MDMOperationTypeRemove, rows[0].OperationType)
		require.Equal(t, automdm.PayloadScopeUser, rows[0].Scope)
	})
}

func TestComputeDeclarationDeltasAssets(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	declsWithBrokenLabel := map[string]struct{}{}

	assetsUpdatedAt := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	newerAssetsUpdatedAt := assetsUpdatedAt.Add(time.Hour)

	// Declaration references an asset; content/token unchanged from what the host
	// already has, but the referenced asset's latest uploaded_at is carried in
	// AssetsUpdatedAt.
	declWithAsset := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID: "aDeclAsset", DeclarationIdentifier: "com.example.asset", DeclarationName: "AssetDecl",
		TeamID: 0, Token: []byte("tokA"), IncludeMode: automdm.AppleProfileIncludeNone,
		AssetsUpdatedAt: &assetsUpdatedAt,
	}
	declsByTeam := map[uint][]*automdm.AppleDeclarationForReconcile{0: {declWithAsset}}

	t.Run("first install stamps assets_updated_at", func(t *testing.T) {
		changedDevice, _, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, nil, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Len(t, rows, 1)
		require.NotNil(t, rows[0].AssetsUpdatedAt)
		require.True(t, assetsUpdatedAt.Equal(*rows[0].AssetsUpdatedAt))
	})

	t.Run("asset unchanged since last delivery -> no diff", func(t *testing.T) {
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclAsset",
				Token:           "tokA",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryVerified),
				AssetsUpdatedAt: &assetsUpdatedAt,
			}},
		}
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)
	})

	t.Run("asset updated (newer uploaded_at) pokes even though token is unchanged", func(t *testing.T) {
		// Host already has the declaration verified with the OLD assets_updated_at
		// and the SAME static token. Only the referenced asset changed.
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclAsset",
				Token:           "tokA",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryVerified),
				AssetsUpdatedAt: &assetsUpdatedAt,
			}},
		}
		declNewerAsset := *declWithAsset
		declNewerAsset.AssetsUpdatedAt = &newerAssetsUpdatedAt
		declsByTeamNewer := map[uint][]*automdm.AppleDeclarationForReconcile{0: {&declNewerAsset}}

		changedDevice, _, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current, declsByTeamNewer, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.MDMOperationTypeInstall, rows[0].OperationType)
		require.Equal(t, "tokA", rows[0].Token)
		require.NotNil(t, rows[0].AssetsUpdatedAt)
		require.True(t, newerAssetsUpdatedAt.Equal(*rows[0].AssetsUpdatedAt))
	})

	t.Run("host missing assets_updated_at but declaration references an asset -> poke", func(t *testing.T) {
		// Simulates a declaration that gained an asset reference: the host row has
		// no assets_updated_at yet, so it must be re-delivered.
		current := map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:        "uuid-A",
				DeclarationUUID: "aDeclAsset",
				Token:           "tokA",
				OperationType:   automdm.MDMOperationTypeInstall,
				Status:          new(automdm.MDMDeliveryVerified),
				AssetsUpdatedAt: nil,
			}},
		}
		changedDevice, _, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current, declsByTeam, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Len(t, rows, 1)
		require.NotNil(t, rows[0].AssetsUpdatedAt)
		require.True(t, assetsUpdatedAt.Equal(*rows[0].AssetsUpdatedAt))
	})
}

func TestComputeDeclarationDeltasActivations(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	declsWithBrokenLabel := map[string]struct{}{}

	activationUpdatedAt := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	// The declaration's own token stays "tokA" throughout: a custom activation
	// lives in its own table, so adding, editing or removing one never changes
	// the declaration's content.
	declWithActivation := &automdm.AppleDeclarationForReconcile{
		DeclarationUUID: "aDeclAct", DeclarationIdentifier: "com.example.act", DeclarationName: "ActDecl",
		TeamID: 0, Token: []byte("tokA"), IncludeMode: automdm.AppleProfileIncludeNone,
		ActivationUpdatedAt: &activationUpdatedAt,
	}
	declNoActivation := *declWithActivation
	declNoActivation.ActivationUpdatedAt = nil

	hostHasActivation := func() map[string][]*automdm.MDMAppleHostDeclaration {
		return map[string][]*automdm.MDMAppleHostDeclaration{
			"uuid-A": {{
				HostUUID:            "uuid-A",
				DeclarationUUID:     "aDeclAct",
				Token:               "tokA",
				OperationType:       automdm.MDMOperationTypeInstall,
				Status:              new(automdm.MDMDeliveryVerified),
				ActivationUpdatedAt: &activationUpdatedAt,
			}},
		}
	}

	t.Run("activation unchanged since last delivery -> no diff", func(t *testing.T) {
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, hostHasActivation(),
			map[uint][]*automdm.AppleDeclarationForReconcile{0: {declWithActivation}}, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)
	})

	t.Run("activation removed pokes and clears the stamp", func(t *testing.T) {
		// Without this the host keeps serving the deleted custom activation,
		// predicate included, because nothing else in the delta changes.
		changedDevice, _, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, hostHasActivation(),
			map[uint][]*automdm.AppleDeclarationForReconcile{0: {&declNoActivation}}, declsWithBrokenLabel,
		)
		require.ElementsMatch(t, []string{"uuid-A"}, changedDevice)
		require.Len(t, rows, 1)
		require.Equal(t, automdm.MDMOperationTypeInstall, rows[0].OperationType)
		require.Equal(t, "tokA", rows[0].Token)
		require.Nil(t, rows[0].ActivationUpdatedAt, "the stamp must clear so the effective token changes")
	})

	t.Run("no activation on either side -> no diff", func(t *testing.T) {
		current := hostHasActivation()
		current["uuid-A"][0].ActivationUpdatedAt = nil
		changedDevice, changedUser, rows := ComputeDeclarationDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil, current,
			map[uint][]*automdm.AppleDeclarationForReconcile{0: {&declNoActivation}}, declsWithBrokenLabel,
		)
		require.Empty(t, changedDevice)
		require.Empty(t, changedUser)
		require.Empty(t, rows)
	})
}

func TestMDMAppleExecuteReconcileBatch(t *testing.T) {
	ctx := context.Background()
	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	pushFactory, _ := newMockAPNSPushProviderFactory()
	pusher := nanomdm_pushsvc.New(
		mdmStorage,
		mdmStorage,
		pushFactory,
		stdlogfmt.New(),
	)
	mdmConfig := config.MDMConfig{
		AppleSCEPCert: "../../service/testdata/server.pem",
		AppleSCEPKey:  "../../service/testdata/server.key",
	}
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		_, pemCert, pemKey, err := mdmConfig.AppleSCEP()
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: pemCert},
			automdm.MDMAssetCAKey:  {Value: pemKey},
		}, nil
	}

	cmdr := NewMDMAppleCommander(mdmStorage, pusher)
	hostUUID1, hostUUID2 := "ABC-DEF", "GHI-JKL"
	hostUUID1UserEnrollment := hostUUID1 + ":user"
	contents1 := []byte("test-content-1")
	expectedContents1 := []byte("test-content-1") // used for AutoMDM variable substitution
	contents2 := []byte("test-content-2")
	contents4 := []byte("test-content-4")
	contents5 := []byte("test-contents-5")
	contents7 := []byte("test-contents-7")

	p1, p2, p3, p4, p5, p6, p7 := "a"+uuid.NewString(), "a"+uuid.NewString(), "a"+uuid.NewString(), "a"+uuid.NewString(), "a"+uuid.NewString(), "a"+uuid.NewString(), "a"+uuid.NewString()
	baseProfilesToInstall := []*automdm.MDMAppleProfilePayload{
		{ProfileUUID: p1, ProfileIdentifier: "com.add.profile", HostUUID: hostUUID1, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p2, ProfileIdentifier: "com.add.profile.two", HostUUID: hostUUID1, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p2, ProfileIdentifier: "com.add.profile.two", HostUUID: hostUUID2, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p4, ProfileIdentifier: "com.add.profile.four", HostUUID: hostUUID2, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p5, ProfileIdentifier: "com.add.profile.five", HostUUID: hostUUID1, Scope: automdm.PayloadScopeUser},
		{ProfileUUID: p5, ProfileIdentifier: "com.add.profile.five", HostUUID: hostUUID2, Scope: automdm.PayloadScopeUser},
		{ProfileUUID: p7, ProfileIdentifier: "com.add.profile.seven", HostUUID: hostUUID1, Scope: automdm.PayloadScopeUser},
		{ProfileUUID: p7, ProfileIdentifier: "com.add.profile.seven", HostUUID: hostUUID2, Scope: automdm.PayloadScopeUser, HostPlatform: "ios"},
	}
	baseProfilesToRemove := []*automdm.MDMAppleProfilePayload{
		{ProfileUUID: p3, ProfileIdentifier: "com.remove.profile", HostUUID: hostUUID1, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p3, ProfileIdentifier: "com.remove.profile", HostUUID: hostUUID2, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: p6, ProfileIdentifier: "com.remove.profile.six", HostUUID: hostUUID1, Scope: automdm.PayloadScopeUser},
		{ProfileUUID: p6, ProfileIdentifier: "com.remove.profile.six", HostUUID: hostUUID2, Scope: automdm.PayloadScopeUser},
	}

	kv.MGetFunc = func(ctx context.Context, keys []string) (map[string]*string, error) {
		return map[string]*string{}, nil
	}

	ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, profileUUIDs []string) (map[string]mobileconfig.Mobileconfig, error) {
		require.ElementsMatch(t, []string{p1, p2, p4, p5, p7}, profileUUIDs)
		// only those profiles that are to be installed
		return map[string]mobileconfig.Mobileconfig{
			p1: contents1,
			p2: contents2,
			p4: contents4,
			p5: contents5,
			p7: contents7,
		}, nil
	}

	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		require.ElementsMatch(t, payload, []*automdm.MDMAppleProfilePayload{{ProfileUUID: p6, ProfileIdentifier: "com.remove.profile.six", HostUUID: hostUUID2, Scope: automdm.PayloadScopeUser}})
		return nil
	}

	ds.GetNanoMDMUserEnrollmentFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoEnrollment, error) {
		if hostUUID == hostUUID1 {
			return &automdm.NanoEnrollment{
				ID:               hostUUID1UserEnrollment,
				DeviceID:         hostUUID1,
				Type:             "User",
				Enabled:          true,
				TokenUpdateTally: 1,
			}, nil
		}
		// hostUUID2 has no user enrollment
		assert.Equal(t, hostUUID2, hostUUID)
		return nil, nil
	}

	mdmStorage.BulkDeleteHostUserCommandsWithoutResultsFunc = func(ctx context.Context, commandToIDs map[string][]string) error {
		require.Empty(t, commandToIDs)
		return nil
	}

	var enqueueFailForOp automdm.MDMOperationType
	var mu sync.Mutex
	mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
		require.NotNil(t, cmd)
		require.NotEmpty(t, cmd.CommandUUID)

		switch cmd.Command.Command.RequestType {
		case "InstallProfile":

			var fullCmd micromdm.CommandPayload
			require.NoError(t, plist.Unmarshal(cmd.Raw, &fullCmd))
			// the p7 library doesn't support concurrent calls to Parse
			mu.Lock()
			pk7, err := pkcs7.Parse(fullCmd.Command.InstallProfile.Payload)
			mu.Unlock()
			require.NoError(t, err)

			if !bytes.Equal(pk7.Content, expectedContents1) && !bytes.Equal(pk7.Content, contents2) &&
				!bytes.Equal(pk7.Content, contents4) && !bytes.Equal(pk7.Content, contents5) && !bytes.Equal(pk7.Content, contents7) {
				require.Failf(t, "profile contents don't match", "expected to contain %s, %s or %s but got %s",
					expectedContents1, contents2, contents4, pk7.Content)
			}

			// may be called for a single host or both
			if len(id) == 2 {
				if bytes.Equal(pk7.Content, contents5) || bytes.Equal(pk7.Content, contents7) {
					require.ElementsMatch(t, []string{hostUUID1UserEnrollment, hostUUID2}, id)
				} else {
					require.ElementsMatch(t, []string{hostUUID1, hostUUID2}, id)
				}
			} else {
				require.Len(t, id, 1)
			}

		case "RemoveProfile":
			if len(id) == 1 {
				require.Equal(t, hostUUID1UserEnrollment, id[0])
			} else {
				require.ElementsMatch(t, []string{hostUUID1, hostUUID2}, id)
			}
			require.Contains(t, string(cmd.Raw), "com.remove.profile")
		}
		switch {
		case enqueueFailForOp == automdm.MDMOperationTypeInstall && cmd.Command.Command.RequestType == "InstallProfile":
			return nil, errors.New("enqueue error")
		case enqueueFailForOp == automdm.MDMOperationTypeRemove && cmd.Command.Command.RequestType == "RemoveProfile":
			return nil, errors.New("enqueue error")
		}
		return nil, nil
	}

	mdmStorage.RetrievePushInfoFunc = func(ctx context.Context, tokens []string) (map[string]*mdm.Push, error) {
		res := make(map[string]*mdm.Push, len(tokens))
		for _, t := range tokens {
			res[t] = &mdm.Push{
				PushMagic: "",
				Token:     []byte(t),
				Topic:     "",
			}
		}
		return res, nil
	}
	mdmStorage.RetrievePushCertFunc = func(ctx context.Context, topic string) (*tls.Certificate, string, error) {
		cert, err := tls.LoadX509KeyPair("../../service/testdata/server.pem", "../../service/testdata/server.key")
		return &cert, "", err
	}
	mdmStorage.IsPushCertStaleFunc = func(ctx context.Context, topic string, staleToken string) (bool, error) {
		return false, nil
	}
	mdmStorage.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		certPEM, err := os.ReadFile("../../service/testdata/server.pem")
		require.NoError(t, err)
		keyPEM, err := os.ReadFile("../../service/testdata/server.key")
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: certPEM},
			automdm.MDMAssetCAKey:  {Value: keyPEM},
		}, nil
	}

	var failedCall bool
	var failedCheck func([]*automdm.MDMAppleBulkUpsertHostProfilePayload)
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		if failedCall {
			failedCheck(payload)
			return nil
		}

		// next call will be failed call, until reset
		failedCall = true

		// first time it is called, it is to set the status to pending and all
		// host profiles have a command uuid
		cmdUUIDByProfileUUIDInstall := make(map[string]string)
		cmdUUIDByProfileUUIDRemove := make(map[string]string)
		copies := make([]*automdm.MDMAppleBulkUpsertHostProfilePayload, len(payload))
		for i, p := range payload {
			// clear the command UUID (in a copy so that it does not affect the
			// pointed-to struct) from the payload for the subsequent checks
			copyp := *p
			copyp.CommandUUID = ""
			copies[i] = &copyp

			// Host with no user enrollment, so install fails
			if p.HostUUID == hostUUID2 && (p.ProfileUUID == p5 || p.ProfileUUID == p7) {
				continue
			}

			if p.OperationType == automdm.MDMOperationTypeInstall {
				existing, ok := cmdUUIDByProfileUUIDInstall[p.ProfileUUID]
				if ok {
					require.Equal(t, existing, p.CommandUUID)
				} else {
					cmdUUIDByProfileUUIDInstall[p.ProfileUUID] = p.CommandUUID
				}
			} else {
				require.Equal(t, automdm.MDMOperationTypeRemove, p.OperationType)
				existing, ok := cmdUUIDByProfileUUIDRemove[p.ProfileUUID]
				if ok {
					require.Equal(t, existing, p.CommandUUID)
				} else {
					cmdUUIDByProfileUUIDRemove[p.ProfileUUID] = p.CommandUUID
				}
			}

		}

		require.ElementsMatch(t, []*automdm.MDMAppleBulkUpsertHostProfilePayload{
			{
				ProfileUUID:       p1,
				ProfileIdentifier: "com.add.profile",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p2,
				ProfileIdentifier: "com.add.profile.two",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p2,
				ProfileIdentifier: "com.add.profile.two",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p3,
				ProfileIdentifier: "com.remove.profile",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeRemove,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p3,
				ProfileIdentifier: "com.remove.profile",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeRemove,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p4,
				ProfileIdentifier: "com.add.profile.four",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			// This host has a user enrollment so the profile is sent to it
			{
				ProfileUUID:       p5,
				ProfileIdentifier: "com.add.profile.five",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeUser,
			},
			// This host has no user enrollment so the profile is errored
			{
				ProfileUUID:       p5,
				ProfileIdentifier: "com.add.profile.five",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Detail:            "This setting couldn't be enforced because the user channel doesn't exist for this host. Currently, AutoMDM creates the user channel for hosts that automatically enroll.",
				Status:            &automdm.MDMDeliveryFailed,
				Scope:             automdm.PayloadScopeUser,
			},
			// This host has a user enrollment so the profile is removed from it
			{
				ProfileUUID:       p6,
				ProfileIdentifier: "com.remove.profile.six",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeRemove,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeUser,
			},
			// Note that host2 has no user enrollment so the profile is not marked for removal
			// from it
			{
				ProfileUUID:       p7,
				ProfileIdentifier: "com.add.profile.seven",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeUser,
			},
			{
				ProfileUUID:       p7,
				ProfileIdentifier: "com.add.profile.seven",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryFailed,
				Detail:            "This setting couldn't be enforced because the user channel isn't available on iOS and iPadOS hosts.",
				Scope:             automdm.PayloadScopeUser,
			},
		}, copies)
		return nil
	}

	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true

	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}

	ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, p []*automdm.MDMAppleConfigProfile) error {
		return nil
	}

	ds.AggregateEnrollSecretPerTeamFunc = func(ctx context.Context) ([]*automdm.EnrollSecret, error) {
		return []*automdm.EnrollSecret{}, nil
	}
	ds.GetMDMAppleReconcileCursorFunc = func(ctx context.Context) (string, error) {
		return "", nil
	}

	checkAndReset := func(t *testing.T, want bool, invoked *bool) {
		if want {
			assert.True(t, *invoked)
		} else {
			assert.False(t, *invoked)
		}
		*invoked = false
	}

	t.Run("success", func(t *testing.T) {
		var failedCount int
		failedCall = false
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			failedCount++
			require.Empty(t, payload)
		}
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, baseProfilesToRemove)
		require.NoError(t, err)
		require.Equal(t, 1, failedCount)
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
	})

	t.Run("fail enqueue remove ops", func(t *testing.T) {
		var failedCount int
		failedCall = false
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			failedCount++
			require.Len(t, payload, 3) // the 3 remove ops
			require.ElementsMatch(t, []*automdm.MDMAppleBulkUpsertHostProfilePayload{
				{
					ProfileUUID:       p3,
					ProfileIdentifier: "com.remove.profile",
					HostUUID:          hostUUID1,
					OperationType:     automdm.MDMOperationTypeRemove,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p3,
					ProfileIdentifier: "com.remove.profile",
					HostUUID:          hostUUID2,
					OperationType:     automdm.MDMOperationTypeRemove,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p6,
					ProfileIdentifier: "com.remove.profile.six",
					HostUUID:          hostUUID1,
					OperationType:     automdm.MDMOperationTypeRemove,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeUser,
				},
			}, payload)
		}

		enqueueFailForOp = automdm.MDMOperationTypeRemove
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, baseProfilesToRemove)
		require.NoError(t, err)
		require.Equal(t, 1, failedCount)
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
	})

	t.Run("fail enqueue install ops", func(t *testing.T) {
		var failedCount int
		failedCall = false
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			failedCount++

			require.Len(t, payload, 6) // the 6 install ops
			require.ElementsMatch(t, []*automdm.MDMAppleBulkUpsertHostProfilePayload{
				{
					ProfileUUID:       p1,
					ProfileIdentifier: "com.add.profile",
					HostUUID:          hostUUID1, OperationType: automdm.MDMOperationTypeInstall,
					Status:      nil,
					CommandUUID: "",
					Scope:       automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p2,
					ProfileIdentifier: "com.add.profile.two",
					HostUUID:          hostUUID1, OperationType: automdm.MDMOperationTypeInstall,
					Status:      nil,
					CommandUUID: "",
					Scope:       automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p2,
					ProfileIdentifier: "com.add.profile.two",
					HostUUID:          hostUUID2,
					OperationType:     automdm.MDMOperationTypeInstall,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p4,
					ProfileIdentifier: "com.add.profile.four",
					HostUUID:          hostUUID2,
					OperationType:     automdm.MDMOperationTypeInstall,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeSystem,
				},
				{
					ProfileUUID:       p5,
					ProfileIdentifier: "com.add.profile.five",
					HostUUID:          hostUUID1,
					OperationType:     automdm.MDMOperationTypeInstall,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeUser,
				},
				{
					ProfileUUID:       p7,
					ProfileIdentifier: "com.add.profile.seven",
					HostUUID:          hostUUID1,
					OperationType:     automdm.MDMOperationTypeInstall,
					Status:            nil,
					CommandUUID:       "",
					Scope:             automdm.PayloadScopeUser,
				},
			}, payload)
		}

		enqueueFailForOp = automdm.MDMOperationTypeInstall
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, baseProfilesToRemove)
		require.NoError(t, err)
		require.Equal(t, 1, failedCount)
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
	})

	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		require.Empty(t, payload)
		return nil
	}
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		if failedCall {
			failedCheck(payload)
			return nil
		}

		// next call will be failed call, until reset
		failedCall = true

		// first time it is called, it is to set the status to pending and all
		// host profiles have a command uuid
		cmdUUIDByProfileUUIDInstall := make(map[string]string)
		cmdUUIDByProfileUUIDRemove := make(map[string]string)
		copies := make([]*automdm.MDMAppleBulkUpsertHostProfilePayload, len(payload))
		for i, p := range payload {
			// clear the command UUID (in a copy so that it does not affect the
			// pointed-to struct) from the payload for the subsequent checks
			copyp := *p
			copyp.CommandUUID = ""
			copies[i] = &copyp

			// Host with no user enrollment, so install fails
			if p.HostUUID == hostUUID2 && (p.ProfileUUID == p5 || p.ProfileUUID == p7) {
				continue
			}

			if p.OperationType == automdm.MDMOperationTypeInstall {
				existing, ok := cmdUUIDByProfileUUIDInstall[p.ProfileUUID]
				if ok {
					require.Equal(t, existing, p.CommandUUID)
				} else {
					cmdUUIDByProfileUUIDInstall[p.ProfileUUID] = p.CommandUUID
				}
			} else {
				require.Equal(t, automdm.MDMOperationTypeRemove, p.OperationType)
				existing, ok := cmdUUIDByProfileUUIDRemove[p.ProfileUUID]
				if ok {
					require.Equal(t, existing, p.CommandUUID)
				} else {
					cmdUUIDByProfileUUIDRemove[p.ProfileUUID] = p.CommandUUID
				}
			}
		}

		require.ElementsMatch(t, []*automdm.MDMAppleBulkUpsertHostProfilePayload{
			{
				ProfileUUID:       p1,
				ProfileIdentifier: "com.add.profile",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p2,
				ProfileIdentifier: "com.add.profile.two",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p2,
				ProfileIdentifier: "com.add.profile.two",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p4,
				ProfileIdentifier: "com.add.profile.four",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeSystem,
			},
			{
				ProfileUUID:       p5,
				ProfileIdentifier: "com.add.profile.five",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeUser,
			},
			// This host has no user enrollment so the profile is sent to the device enrollment
			{
				ProfileUUID:       p5,
				ProfileIdentifier: "com.add.profile.five",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryFailed,
				Detail:            "This setting couldn't be enforced because the user channel doesn't exist for this host. Currently, AutoMDM creates the user channel for hosts that automatically enroll.",
				Scope:             automdm.PayloadScopeUser,
			},
			{
				ProfileUUID:       p7,
				ProfileIdentifier: "com.add.profile.seven",
				HostUUID:          hostUUID1,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryPending,
				Scope:             automdm.PayloadScopeUser,
			},
			{
				ProfileUUID:       p7,
				ProfileIdentifier: "com.add.profile.seven",
				HostUUID:          hostUUID2,
				OperationType:     automdm.MDMOperationTypeInstall,
				Status:            &automdm.MDMDeliveryFailed,
				Detail:            "This setting couldn't be enforced because the user channel isn't available on iOS and iPadOS hosts.",
				Scope:             automdm.PayloadScopeUser,
			},
		}, copies)
		return nil
	}

	ctx = license.NewContext(ctx, &automdm.LicenseInfo{Tier: automdm.TierPremium})
	ds.BulkUpsertMDMManagedCertificatesFunc = func(ctx context.Context, payload []*automdm.MDMManagedCertificate) error {
		assert.Empty(t, payload)
		return nil
	}

	t.Run("replace $AUTOMDM_VAR_"+string(automdm.AutoMDMVarNDESSCEPProxyURL), func(t *testing.T) {
		var upsertCount int
		failedCall = false
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			upsertCount++
			if upsertCount == 1 {
				// We update the profile with a new command UUID
				assert.Len(t, payload, 1, "at upsertCount %d", upsertCount)
			} else {
				assert.Empty(t, payload, "at upsertCount %d", upsertCount)
			}
		}
		enqueueFailForOp = ""
		newContents := "$AUTOMDM_VAR_" + automdm.AutoMDMVarNDESSCEPProxyURL
		originalContents1 := contents1
		originalExpectedContents1 := expectedContents1
		contents1 = []byte(newContents)
		expectedContents1 = []byte("https://test.example.com" + SCEPProxyPath + url.QueryEscape(fmt.Sprintf("%s,%s,NDES", hostUUID1, p1)))
		t.Cleanup(func() {
			contents1 = originalContents1
			expectedContents1 = originalExpectedContents1
		})
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, upsertCount)
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
	})

	t.Run("preprocessor fails on $AUTOMDM_VAR_"+string(automdm.AutoMDMVarHostHardwareSerial), func(t *testing.T) {
		var failedCount int
		failedCall = false
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			failedCount++
			require.Len(t, payload, 8)
		}
		enqueueFailForOp = ""
		newContents := "$AUTOMDM_VAR_" + automdm.AutoMDMVarHostHardwareSerial
		originalContents1 := contents1
		contents1 = []byte(newContents)
		t.Cleanup(func() {
			contents1 = originalContents1
			ds.ListHostsLiteByUUIDsFunc = nil
		})
		ds.ListHostsLiteByUUIDsFunc = func(ctx context.Context, filter automdm.TeamFilter, uuids []string) ([]*automdm.Host, error) {
			return nil, errors.New("ListHostsLiteByUUIDsFuncError")
		}
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, nil)
		require.ErrorContains(t, err, "ListHostsLiteByUUIDsFuncError")
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
	})

	t.Run("bad $AUTOMDM_VAR", func(t *testing.T) {
		var failedCount int
		failedCall = false
		var hostUUIDs []string
		failedCheck = func(payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) {
			if len(payload) > 0 {
				failedCount++
			}
			for _, p := range payload {
				assert.Equal(t, automdm.MDMDeliveryFailed, *p.Status)
				assert.Contains(t, p.Detail, "AUTOMDM_VAR_BOZO")
				for i, hu := range hostUUIDs {
					if hu == p.HostUUID {
						// remove element
						hostUUIDs = append(hostUUIDs[:i], hostUUIDs[i+1:]...)
						break
					}
				}
			}
		}
		enqueueFailForOp = ""

		// All profiles will have bad contents
		badContents := "bad-content: $AUTOMDM_VAR_BOZO"
		originalContents1 := contents1
		originalContents2 := contents2
		originalContents4 := contents4
		originalContents5 := contents5
		originalContents7 := contents7
		contents1 = []byte(badContents)
		contents2 = []byte(badContents)
		contents4 = []byte(badContents)
		contents5 = []byte(badContents)
		contents7 = []byte(badContents)
		t.Cleanup(func() {
			contents1 = originalContents1
			contents2 = originalContents2
			contents4 = originalContents4
			contents5 = originalContents5
			contents7 = originalContents7
		})

		hostUUIDs = make([]string, 0, len(baseProfilesToInstall))
		for _, p := range baseProfilesToInstall {
			// This host will error before this point - should not be updated by the variable failure
			if p.HostUUID == hostUUID2 && (p.ProfileUUID == p5 || p.ProfileUUID == p7) {
				continue
			}
			hostUUIDs = append(hostUUIDs, p.HostUUID)
		}

		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, baseProfilesToInstall, nil)
		require.NoError(t, err)
		assert.Empty(t, hostUUIDs, "all host+profile combinations should be updated")
		require.Equal(t, 5, failedCount, "number of profiles with bad content")
		checkAndReset(t, true, &ds.GetMDMAppleProfilesContentsFuncInvoked)
		checkAndReset(t, true, &ds.BulkUpsertMDMAppleHostProfilesFuncInvoked)
		checkAndReset(t, true, &ds.GetNanoMDMUserEnrollmentFuncInvoked)
		// Check that individual updates were not done (bulk update should be done)
		checkAndReset(t, false, &ds.UpdateOrDeleteHostMDMAppleProfileFuncInvoked)
	})
}

func TestMDMAppleExecuteReconcileBatchDedupesEnrollmentIDs(t *testing.T) {
	// Backstop: even if ComputeReconcileDeltas hands ExecuteReconcileBatch two
	// payloads for the same profile+host UUID (as duplicate host rows sharing a
	// UUID produce), the enrollment ID must reach the enqueue exactly once — a
	// repeated ID collides on the nano_enrollment_queue (id, command_uuid) PK.
	ctx := context.Background()
	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	pushFactory, _ := newMockAPNSPushProviderFactory()
	pusher := nanomdm_pushsvc.New(mdmStorage, mdmStorage, pushFactory, stdlogfmt.New())
	cmdr := NewMDMAppleCommander(mdmStorage, pusher)

	const hostUUID = "DUP-UUID"
	profUUID := "a" + uuid.NewString()
	toInstall := []*automdm.MDMAppleProfilePayload{
		{ProfileUUID: profUUID, ProfileIdentifier: "com.dup.profile", HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: profUUID, ProfileIdentifier: "com.dup.profile", HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem},
	}

	kv.MGetFunc = func(ctx context.Context, keys []string) (map[string]*string, error) {
		return map[string]*string{}, nil
	}
	ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, profileUUIDs []string) (map[string]mobileconfig.Mobileconfig, error) {
		return map[string]mobileconfig.Mobileconfig{profUUID: []byte("dup-content")}, nil
	}
	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}
	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		return nil
	}
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		return nil
	}

	var mu sync.Mutex
	var enqueuedIDs [][]string
	mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
		mu.Lock()
		enqueuedIDs = append(enqueuedIDs, append([]string(nil), id...))
		mu.Unlock()
		return nil, nil
	}
	mdmStorage.RetrievePushInfoFunc = func(ctx context.Context, tokens []string) (map[string]*mdm.Push, error) {
		res := make(map[string]*mdm.Push, len(tokens))
		for _, tok := range tokens {
			res[tok] = &mdm.Push{Token: []byte(tok)}
		}
		return res, nil
	}
	mdmStorage.RetrievePushCertFunc = func(ctx context.Context, topic string) (*tls.Certificate, string, error) {
		cert, err := tls.LoadX509KeyPair("../../service/testdata/server.pem", "../../service/testdata/server.key")
		return &cert, "", err
	}
	mdmStorage.IsPushCertStaleFunc = func(ctx context.Context, topic string, staleToken string) (bool, error) {
		return false, nil
	}
	mdmStorage.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		certPEM, err := os.ReadFile("../../service/testdata/server.pem")
		require.NoError(t, err)
		keyPEM, err := os.ReadFile("../../service/testdata/server.key")
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: certPEM},
			automdm.MDMAssetCAKey:  {Value: keyPEM},
		}, nil
	}

	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true

	succeeded, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, toInstall, nil)
	require.NoError(t, err)
	require.Len(t, succeeded, 1)

	require.Len(t, enqueuedIDs, 1)
	require.Equal(t, []string{hostUUID}, enqueuedIDs[0])
}

func TestMDMAppleExecuteReconcileBatchCAThrottle(t *testing.T) {
	ctx := t.Context()
	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	pushFactory, _ := newMockAPNSPushProviderFactory()
	pusher := nanomdm_pushsvc.New(
		mdmStorage,
		mdmStorage,
		pushFactory,
		stdlogfmt.New(),
	)
	mdmConfig := config.MDMConfig{
		AppleSCEPCert: "../../service/testdata/server.pem",
		AppleSCEPKey:  "../../service/testdata/server.key",
	}
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		_, pemCert, pemKey, err := mdmConfig.AppleSCEP()
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: pemCert},
			automdm.MDMAssetCAKey:  {Value: pemKey},
		}, nil
	}

	cmdr := NewMDMAppleCommander(mdmStorage, pusher)
	hostUUIDs := []string{"host-1", "host-2", "host-3", "host-4", "host-5"}

	caProfileUUID := "a" + uuid.NewString()
	nonCAProfileUUID := "a" + uuid.NewString()
	caContent := []byte("profile with $AUTOMDM_VAR_NDES_SCEP_CHALLENGE variable")
	nonCAContent := []byte("regular profile content")

	appCfg := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}

	// Build toInstall: CA profile for 5 hosts + non-CA profile for 5 hosts
	var profilesToInstall []*automdm.MDMAppleProfilePayload
	for _, h := range hostUUIDs {
		profilesToInstall = append(
			profilesToInstall,
			&automdm.MDMAppleProfilePayload{ProfileUUID: caProfileUUID, ProfileIdentifier: "com.ca.profile", ProfileName: "CA Profile", HostUUID: h, Scope: automdm.PayloadScopeSystem},
			&automdm.MDMAppleProfilePayload{ProfileUUID: nonCAProfileUUID, ProfileIdentifier: "com.regular.profile", ProfileName: "Regular Profile", HostUUID: h, Scope: automdm.PayloadScopeSystem},
		)
	}

	ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, profileUUIDs []string) (map[string]mobileconfig.Mobileconfig, error) {
		return map[string]mobileconfig.Mobileconfig{
			caProfileUUID:    caContent,
			nonCAProfileUUID: nonCAContent,
		}, nil
	}

	kv.MGetFunc = func(ctx context.Context, keys []string) (map[string]*string, error) {
		return make(map[string]*string), nil
	}

	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		return nil
	}

	ds.GetNanoMDMUserEnrollmentFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoEnrollment, error) {
		return nil, nil
	}

	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, allCAs bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}

	mdmStorage.BulkDeleteHostUserCommandsWithoutResultsFunc = func(ctx context.Context, commandToIDs map[string][]string) error {
		return nil
	}

	mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
		return nil, nil
	}

	mdmStorage.RetrievePushInfoFunc = func(ctx context.Context, tokens []string) (map[string]*mdm.Push, error) {
		res := make(map[string]*mdm.Push, len(tokens))
		for _, t := range tokens {
			res[t] = &mdm.Push{
				PushMagic: "",
				Token:     []byte(t),
				Topic:     "",
			}
		}
		return res, nil
	}
	mdmStorage.RetrievePushCertFunc = func(ctx context.Context, topic string) (*tls.Certificate, string, error) {
		cert, err := tls.LoadX509KeyPair("../../service/testdata/server.pem", "../../service/testdata/server.key")
		return &cert, "", err
	}
	mdmStorage.IsPushCertStaleFunc = func(ctx context.Context, topic string, staleToken string) (bool, error) {
		return false, nil
	}
	mdmStorage.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		certPEM, err := os.ReadFile("../../service/testdata/server.pem")
		require.NoError(t, err)
		keyPEM, err := os.ReadFile("../../service/testdata/server.key")
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: certPEM},
			automdm.MDMAssetCAKey:  {Value: keyPEM},
		}, nil
	}

	ds.AggregateEnrollSecretPerTeamFunc = func(ctx context.Context) ([]*automdm.EnrollSecret, error) {
		return []*automdm.EnrollSecret{}, nil
	}

	ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, p []*automdm.MDMAppleConfigProfile) error {
		return nil
	}

	// Track upserted host profiles to verify throttling.
	// The first BulkUpsert call contains the profiles that will be sent;
	// subsequent calls are for reverting failures (empty).
	var upsertedProfiles []*automdm.MDMAppleBulkUpsertHostProfilePayload
	var bulkUpsertCallCount int
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		bulkUpsertCallCount++
		if bulkUpsertCallCount == 1 {
			upsertedProfiles = payload
		}
		return nil
	}

	t.Run("limit=0 sends all profiles", func(t *testing.T) {
		upsertedProfiles = nil
		bulkUpsertCallCount = 0
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, profilesToInstall, nil)
		require.NoError(t, err)

		// All 10 host-profile pairs should be upserted (5 CA + 5 non-CA)
		var caCount, nonCACount int
		for _, p := range upsertedProfiles {
			if p.ProfileUUID == caProfileUUID {
				caCount++
			} else if p.ProfileUUID == nonCAProfileUUID {
				nonCACount++
			}
		}
		assert.Equal(t, 5, caCount, "all CA host-profile pairs should be sent when limit=0")
		assert.Equal(t, 5, nonCACount, "all non-CA host-profile pairs should be sent")
	})

	t.Run("limit=2 throttles CA profiles only", func(t *testing.T) {
		upsertedProfiles = nil
		bulkUpsertCallCount = 0
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, new(2), profilesToInstall, nil)
		require.NoError(t, err)

		// Should have 2 CA + 5 non-CA = 7 host-profile pairs upserted
		var caCount, nonCACount int
		for _, p := range upsertedProfiles {
			if p.ProfileUUID == caProfileUUID {
				caCount++
			} else if p.ProfileUUID == nonCAProfileUUID {
				nonCACount++
			}
		}
		assert.Equal(t, 2, caCount, "only 2 CA host-profile pairs should be sent when limit=2")
		assert.Equal(t, 5, nonCACount, "all non-CA host-profile pairs should still be sent")
	})

	t.Run("one budget shared across calls throttles across the whole tick", func(t *testing.T) {
		// The batched cron drains several windows per tick, calling this function once per window. The budget is shared so
		// mdm.certificate_profiles_limit stays the per-tick rate its config description promises; a per-call limit would
		// multiply CA issuance against the customer's CA by the number of windows drained.
		caBudget := new(3)

		upsertedProfiles = nil
		bulkUpsertCallCount = 0
		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, caBudget, profilesToInstall, nil)
		require.NoError(t, err)
		firstCA := 0
		for _, p := range upsertedProfiles {
			if p.ProfileUUID == caProfileUUID {
				firstCA++
			}
		}
		assert.Equal(t, 3, firstCA, "the first window spends the whole budget")
		assert.Equal(t, 0, *caBudget, "the budget is decremented in place")

		upsertedProfiles = nil
		bulkUpsertCallCount = 0
		_, err = ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, caBudget, profilesToInstall, nil)
		require.NoError(t, err)
		secondCA, secondNonCA := 0, 0
		for _, p := range upsertedProfiles {
			switch p.ProfileUUID {
			case caProfileUUID:
				secondCA++
			case nonCAProfileUUID:
				secondNonCA++
			}
		}
		assert.Equal(t, 0, secondCA, "the exhausted budget must throttle the next window, not reset for it")
		assert.Equal(t, 5, secondNonCA, "non-CA profiles are never throttled")
	})

	t.Run("recently enrolled hosts bypass throttle", func(t *testing.T) {
		upsertedProfiles = nil
		bulkUpsertCallCount = 0

		recentEnrollTime := time.Now().Add(-30 * time.Minute)
		var recentProfilesToInstall []*automdm.MDMAppleProfilePayload
		for _, h := range hostUUIDs {
			recentProfilesToInstall = append(
				recentProfilesToInstall,
				&automdm.MDMAppleProfilePayload{
					ProfileUUID: caProfileUUID, ProfileIdentifier: "com.ca.profile", ProfileName: "CA Profile",
					HostUUID: h, Scope: automdm.PayloadScopeSystem, DeviceEnrolledAt: &recentEnrollTime,
				},
				&automdm.MDMAppleProfilePayload{
					ProfileUUID: nonCAProfileUUID, ProfileIdentifier: "com.regular.profile", ProfileName: "Regular Profile",
					HostUUID: h, Scope: automdm.PayloadScopeSystem, DeviceEnrolledAt: &recentEnrollTime,
				},
			)
		}

		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, new(2), recentProfilesToInstall, nil)
		require.NoError(t, err)

		var caCount, nonCACount int
		for _, p := range upsertedProfiles {
			switch p.ProfileUUID {
			case caProfileUUID:
				caCount++
			case nonCAProfileUUID:
				nonCACount++
			}
		}
		assert.Equal(t, 5, caCount, "all CA host-profile pairs should be sent for recently enrolled hosts")
		assert.Equal(t, 5, nonCACount, "all non-CA host-profile pairs should be sent")
	})

	t.Run("removals are not throttled", func(t *testing.T) {
		upsertedProfiles = nil
		bulkUpsertCallCount = 0

		var profilesToRemove []*automdm.MDMAppleProfilePayload
		for _, h := range hostUUIDs {
			profilesToRemove = append(
				profilesToRemove,
				&automdm.MDMAppleProfilePayload{
					ProfileUUID: caProfileUUID, ProfileIdentifier: "com.ca.profile", ProfileName: "CA Profile",
					HostUUID: h, Scope: automdm.PayloadScopeSystem, OperationType: automdm.MDMOperationTypeInstall,
					Status: &automdm.MDMDeliveryVerifying, CommandUUID: uuid.NewString(),
				},
			)
		}

		_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, new(2), nil, profilesToRemove)
		require.NoError(t, err)

		var removeCount int
		for _, p := range upsertedProfiles {
			if p.ProfileUUID == caProfileUUID && p.OperationType == automdm.MDMOperationTypeRemove {
				removeCount++
			}
		}
		assert.Equal(t, 5, removeCount, "all CA profile removals should proceed regardless of throttle limit")
	})
}

func TestMDMAppleExecuteReconcileBatchSkipsHostBeingProcessed(t *testing.T) {
	ctx := t.Context()
	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	pushFactory, _ := newMockAPNSPushProviderFactory()
	pusher := nanomdm_pushsvc.New(
		mdmStorage,
		mdmStorage,
		pushFactory,
		stdlogfmt.New(),
	)
	mdmConfig := config.MDMConfig{
		AppleSCEPCert: "../../service/testdata/server.pem",
		AppleSCEPKey:  "../../service/testdata/server.key",
	}
	ds.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		_, pemCert, pemKey, err := mdmConfig.AppleSCEP()
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: pemCert},
			automdm.MDMAssetCAKey:  {Value: pemKey},
		}, nil
	}

	cmdr := NewMDMAppleCommander(mdmStorage, pusher)

	profileUUID := "a" + uuid.NewString()
	profileContent := []byte("regular profile content")
	blockedHostUUID := "host-blocked"
	nonSetupHostUUID := "host-non-setup"

	appCfg := &automdm.AppConfig{MDM: automdm.MDM{EnabledAndConfigured: true}}
	profilesToInstall := []*automdm.MDMAppleProfilePayload{
		{ProfileUUID: profileUUID, ProfileIdentifier: "com.test.profile", ProfileName: "Test Profile", HostUUID: blockedHostUUID, Scope: automdm.PayloadScopeSystem},
		{ProfileUUID: profileUUID, ProfileIdentifier: "com.test.profile", ProfileName: "Test Profile", HostUUID: nonSetupHostUUID, Scope: automdm.PayloadScopeSystem},
	}
	ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, profileUUIDs []string) (map[string]mobileconfig.Mobileconfig, error) {
		return map[string]mobileconfig.Mobileconfig{profileUUID: profileContent}, nil
	}
	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		return nil
	}
	ds.GetNanoMDMUserEnrollmentFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoEnrollment, error) {
		return nil, nil
	}
	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, allCAs bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}
	ds.AggregateEnrollSecretPerTeamFunc = func(ctx context.Context) ([]*automdm.EnrollSecret, error) {
		return []*automdm.EnrollSecret{}, nil
	}
	ds.BulkUpsertMDMAppleConfigProfilesFunc = func(ctx context.Context, p []*automdm.MDMAppleConfigProfile) error {
		return nil
	}
	mdmStorage.BulkDeleteHostUserCommandsWithoutResultsFunc = func(ctx context.Context, commandToIDs map[string][]string) error {
		return nil
	}
	mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
		return nil, nil
	}
	mdmStorage.RetrievePushInfoFunc = func(ctx context.Context, tokens []string) (map[string]*mdm.Push, error) {
		res := make(map[string]*mdm.Push, len(tokens))
		for _, t := range tokens {
			res[t] = &mdm.Push{PushMagic: "", Token: []byte(t), Topic: ""}
		}
		return res, nil
	}
	mdmStorage.RetrievePushCertFunc = func(ctx context.Context, topic string) (*tls.Certificate, string, error) {
		cert, err := tls.LoadX509KeyPair("../../service/testdata/server.pem", "../../service/testdata/server.key")
		return &cert, "", err
	}
	mdmStorage.IsPushCertStaleFunc = func(ctx context.Context, topic string, staleToken string) (bool, error) {
		return false, nil
	}
	mdmStorage.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, assetNames []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		certPEM, err := os.ReadFile("../../service/testdata/server.pem")
		require.NoError(t, err)
		keyPEM, err := os.ReadFile("../../service/testdata/server.key")
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: certPEM},
			automdm.MDMAssetCAKey:  {Value: keyPEM},
		}, nil
	}

	// Track what gets upserted and which hosts get commands enqueued
	var upsertedProfiles []*automdm.MDMAppleBulkUpsertHostProfilePayload
	var bulkUpsertCallCount int
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		bulkUpsertCallCount++
		if bulkUpsertCallCount == 1 {
			upsertedProfiles = payload
		}
		return nil
	}

	// Simulate an in-memory KV store with TTL support
	kvStore := make(map[string]string)
	kv.MGetFunc = func(ctx context.Context, keys []string) (map[string]*string, error) {
		result := make(map[string]*string, len(keys))
		for _, k := range keys {
			if v, ok := kvStore[k]; ok {
				result[k] = &v
			} else {
				result[k] = nil
			}
		}
		return result, nil
	}

	// verify host marked as going through setup does not get profiles reconciled
	blockedKey := automdm.MDMProfileProcessingKeyPrefix + ":" + blockedHostUUID
	kvStore[blockedKey] = "1"

	upsertedProfiles = nil
	bulkUpsertCallCount = 0
	_, err := ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, profilesToInstall, nil)
	require.NoError(t, err)

	// Only the non setup host should have profiles with a pending status and command UUID;
	// the blocked host should have its status/command cleared.
	var pendingHosts []string
	var skippedHosts []string
	for _, p := range upsertedProfiles {
		if p.Status != nil && *p.Status == automdm.MDMDeliveryPending && p.CommandUUID != "" {
			pendingHosts = append(pendingHosts, p.HostUUID)
		} else if p.Status == nil && p.CommandUUID == "" {
			skippedHosts = append(skippedHosts, p.HostUUID)
		}
	}
	assert.Contains(t, pendingHosts, nonSetupHostUUID, "non setup host should have profiles enqueued")
	assert.NotContains(t, pendingHosts, blockedHostUUID, "blocked host should NOT have profiles enqueued")
	assert.Contains(t, skippedHosts, blockedHostUUID, "blocked host should be skipped with nil status")

	// expire the key, the host that didn't get profiles before should do now
	delete(kvStore, blockedKey) // simulate TTL expiry

	upsertedProfiles = nil
	bulkUpsertCallCount = 0
	_, err = ExecuteReconcileBatch(ctx, ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, profilesToInstall, nil)
	require.NoError(t, err)

	pendingHosts = nil
	for _, p := range upsertedProfiles {
		if p.Status != nil && *p.Status == automdm.MDMDeliveryPending && p.CommandUUID != "" {
			pendingHosts = append(pendingHosts, p.HostUUID)
		}
	}
	assert.Contains(t, pendingHosts, nonSetupHostUUID, "non setup host should still have profiles enqueued")
	assert.Contains(t, pendingHosts, blockedHostUUID, "previously blocked host should now have profiles enqueued after key expiry")
}

// Deleting a profile and adding the same one back mints a new profile UUID, so the
// host carries two rows for one identifier: the old one mid-removal and the new one
// awaiting install. The queued RemoveProfile must be cancelled or it strips the
// profile the admin just re-added. See issue #49573.
func TestComputeReconcileDeltasCancelsSupersededRemoval(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	readded := &automdm.AppleProfileForReconcile{
		ProfileUUID:       "aNewProfileUUID",
		ProfileIdentifier: "com.example.wifi",
		ProfileName:       "Wi-Fi",
		TeamID:            0,
		Checksum:          []byte("aaaa"),
		IncludeMode:       automdm.AppleProfileIncludeNone,
	}
	staleRemoval := &automdm.MDMAppleProfilePayload{
		ProfileUUID:       "aOldProfileUUID",
		ProfileIdentifier: "com.example.wifi",
		ProfileName:       "Wi-Fi",
		HostUUID:          "uuid-A",
		Checksum:          []byte("aaaa"),
		OperationType:     automdm.MDMOperationTypeRemove,
		Status:            &automdm.MDMDeliveryPending,
		CommandUUID:       "remove-cmd",
	}
	current := map[string][]*automdm.MDMAppleProfilePayload{"uuid-A": {staleRemoval}}

	t.Run("identifier re-added: removal carried through as cancel-only", func(t *testing.T) {
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil,
			current,
			map[uint][]*automdm.AppleProfileForReconcile{0: {readded}},
			map[string]struct{}{}, nil,
		)

		require.Len(t, toInstall, 1)
		require.Equal(t, "aNewProfileUUID", toInstall[0].ProfileUUID)

		require.Len(t, toRemove, 1)
		require.Equal(t, "aOldProfileUUID", toRemove[0].ProfileUUID)
		require.True(t, toRemove[0].CancelOnly)
		require.Equal(t, "remove-cmd", toRemove[0].CommandUUID)
	})

	t.Run("identifier not re-added: removal still left alone", func(t *testing.T) {
		toInstall, toRemove, _ := ComputeReconcileDeltas(
			[]*automdm.AppleHostReconcileInfo{host}, nil,
			current,
			map[uint][]*automdm.AppleProfileForReconcile{}, // nothing desired
			map[string]struct{}{}, nil,
		)

		require.Empty(t, toInstall)
		require.Empty(t, toRemove, "an in-flight removal must not be queued a second time")
	})
}

// End of the same flow: the cancel-only removal must pull its queued command out
// without ever sending a RemoveProfile, and must be left untouched when the install
// that justified it did not survive the callers' filters.
type reconcileBatchResults struct {
	deletedCmds map[string][]string
	enqueued    []string
	cleanedUp   []*automdm.MDMAppleProfilePayload
	upserted    []*automdm.MDMAppleBulkUpsertHostProfilePayload
	err         error
}

// runReconcileBatchCapturing runs ExecuteReconcileBatch against mocks and
// records its side effects. Hosts in beingSetUp are reported as having the
// profile-processing key set; overrides run after the default mocks are set.
func runReconcileBatchCapturing(t *testing.T, beingSetUp []string, toInstall, toRemove []*automdm.MDMAppleProfilePayload,
	overrides ...func(ds *mock.Store),
) *reconcileBatchResults {
	t.Helper()
	res := &reconcileBatchResults{deletedCmds: map[string][]string{}}

	mdmStorage := &mdmmock.MDMAppleStore{}
	ds := new(mock.Store)
	kv := new(mock.AdvancedKVStore)
	pushFactory, _ := newMockAPNSPushProviderFactory()
	cmdr := NewMDMAppleCommander(mdmStorage, nanomdm_pushsvc.New(mdmStorage, mdmStorage, pushFactory, stdlogfmt.New()))

	kv.MGetFunc = func(ctx context.Context, keys []string) (map[string]*string, error) {
		out := map[string]*string{}
		for _, h := range beingSetUp {
			out[automdm.MDMProfileProcessingKeyPrefix+":"+h] = new("1")
		}
		return out, nil
	}
	ds.GetNanoMDMUserEnrollmentFunc = func(ctx context.Context, hostUUID string) (*automdm.NanoEnrollment, error) {
		return nil, nil
	}
	ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, uuids []string) (map[string]mobileconfig.Mobileconfig, error) {
		out := make(map[string]mobileconfig.Mobileconfig, len(uuids))
		for _, u := range uuids {
			out[u] = []byte("content")
		}
		return out, nil
	}
	ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
		return &automdm.GroupedCertificateAuthorities{}, nil
	}
	ds.BulkDeleteMDMAppleHostsConfigProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleProfilePayload) error {
		res.cleanedUp = append(res.cleanedUp, payload...)
		return nil
	}
	ds.BulkUpsertMDMAppleHostProfilesFunc = func(ctx context.Context, payload []*automdm.MDMAppleBulkUpsertHostProfilePayload) error {
		res.upserted = append(res.upserted, payload...)
		return nil
	}
	ds.ListMDMManagedCertificatesForHostProfilesFunc = func(ctx context.Context, hostProfiles []automdm.HostProfileUUID) ([]*automdm.MDMManagedCertificate, error) {
		return nil, nil
	}

	var mu sync.Mutex
	mdmStorage.BulkDeleteHostUserCommandsWithoutResultsFunc = func(ctx context.Context, commandToIDs map[string][]string) error {
		mu.Lock()
		defer mu.Unlock()
		for cmd, ids := range commandToIDs {
			res.deletedCmds[cmd] = append(res.deletedCmds[cmd], ids...)
		}
		return nil
	}
	mdmStorage.EnqueueCommandFunc = func(ctx context.Context, id []string, cmd *mdm.CommandWithSubtype) (map[string]error, error) {
		mu.Lock()
		defer mu.Unlock()
		res.enqueued = append(res.enqueued, cmd.Command.Command.RequestType)
		return nil, nil
	}
	mdmStorage.RetrievePushInfoFunc = func(ctx context.Context, tokens []string) (map[string]*mdm.Push, error) {
		out := make(map[string]*mdm.Push, len(tokens))
		for _, tok := range tokens {
			out[tok] = &mdm.Push{Token: []byte(tok)}
		}
		return out, nil
	}
	mdmStorage.RetrievePushCertFunc = func(ctx context.Context, topic string) (*tls.Certificate, string, error) {
		cert, err := tls.LoadX509KeyPair("../../service/testdata/server.pem", "../../service/testdata/server.key")
		return &cert, "", err
	}
	mdmStorage.IsPushCertStaleFunc = func(ctx context.Context, topic string, staleToken string) (bool, error) {
		return false, nil
	}
	mdmStorage.GetAllMDMConfigAssetsByNameFunc = func(ctx context.Context, names []automdm.MDMAssetName,
		_ sqlx.QueryerContext,
	) (map[automdm.MDMAssetName]automdm.MDMConfigAsset, error) {
		certPEM, err := os.ReadFile("../../service/testdata/server.pem")
		require.NoError(t, err)
		keyPEM, err := os.ReadFile("../../service/testdata/server.key")
		require.NoError(t, err)
		return map[automdm.MDMAssetName]automdm.MDMConfigAsset{
			automdm.MDMAssetCACert: {Value: certPEM},
			automdm.MDMAssetCAKey:  {Value: keyPEM},
		}, nil
	}

	appCfg := &automdm.AppConfig{}
	appCfg.ServerSettings.ServerURL = "https://test.example.com"
	appCfg.MDM.EnabledAndConfigured = true

	for _, override := range overrides {
		override(ds)
	}

	_, res.err = ExecuteReconcileBatch(t.Context(), ds, cmdr, kv, slog.New(slog.DiscardHandler), appCfg, nil, toInstall, toRemove)
	return res
}

func TestMDMAppleExecuteReconcileBatchCancelOnlyRemoval(t *testing.T) {
	const hostUUID = "uuid-A"
	const identifier = "com.example.wifi"

	newInstall := func() *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aNewProfileUUID", ProfileIdentifier: identifier, ProfileName: "Wi-Fi",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte("aaaa"),
		}
	}
	cancelOnly := func() *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aOldProfileUUID", ProfileIdentifier: identifier, ProfileName: "Wi-Fi",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte("aaaa"),
			OperationType: automdm.MDMOperationTypeRemove, Status: &automdm.MDMDeliveryPending,
			CommandUUID: "remove-cmd", CancelOnly: true,
		}
	}

	t.Run("install present: removal cancelled, no RemoveProfile sent", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil, []*automdm.MDMAppleProfilePayload{newInstall()}, []*automdm.MDMAppleProfilePayload{cancelOnly()})
		require.NoError(t, res.err)

		require.Contains(t, res.deletedCmds, "remove-cmd")
		require.Equal(t, []string{hostUUID}, res.deletedCmds["remove-cmd"])
		require.Equal(t, []string{"InstallProfile"}, res.enqueued)

		require.Len(t, res.cleanedUp, 1)
		require.Equal(t, "aOldProfileUUID", res.cleanedUp[0].ProfileUUID)

		// the re-added profile must be installed, not left carrying the removal's state
		require.Len(t, res.upserted, 1)
		require.Equal(t, "aNewProfileUUID", res.upserted[0].ProfileUUID)
		require.Equal(t, automdm.MDMOperationTypeInstall, res.upserted[0].OperationType)
		require.NotEmpty(t, res.upserted[0].CommandUUID)
	})

	t.Run("install filtered out: removal left untouched", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil, nil, []*automdm.MDMAppleProfilePayload{cancelOnly()})
		require.NoError(t, res.err)

		require.Empty(t, res.deletedCmds, "nothing to supersede, so the queued command must stand")
		require.Empty(t, res.enqueued, "a cancel-only row must never send a RemoveProfile")
		require.Empty(t, res.cleanedUp)
	})
}

func TestMDMAppleExecuteReconcileBatchRemovesUnsentInstall(t *testing.T) {
	const hostUUID = "uuid-A"

	row := func(status *automdm.MDMDeliveryStatus, cmdUUID string) *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aDeletedProfileUUID", ProfileIdentifier: "com.example.wifi", ProfileName: "Wi-Fi",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte("aaaa"),
			OperationType: automdm.MDMOperationTypeInstall, Status: status, CommandUUID: cmdUUID,
		}
	}

	t.Run("install not sent (e.g. resend): RemoveProfile sent, error tolerated", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil, nil, []*automdm.MDMAppleProfilePayload{row(nil, "")})
		require.NoError(t, res.err)

		require.Equal(t, []string{"RemoveProfile"}, res.enqueued)
		require.Empty(t, res.deletedCmds)
		require.Empty(t, res.cleanedUp)
		require.Len(t, res.upserted, 1)
		require.Equal(t, automdm.MDMOperationTypeRemove, res.upserted[0].OperationType)
		require.True(t, res.upserted[0].IgnoreError)
	})

	t.Run("install failed without verification: RemoveProfile sent, row kept", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil, nil, []*automdm.MDMAppleProfilePayload{row(&automdm.MDMDeliveryFailed, "install-cmd")})
		require.NoError(t, res.err)

		require.Equal(t, []string{"RemoveProfile"}, res.enqueued)
		require.Empty(t, res.cleanedUp)
		require.Len(t, res.upserted, 1)
		require.Equal(t, automdm.MDMOperationTypeRemove, res.upserted[0].OperationType)
		require.True(t, res.upserted[0].IgnoreError)
	})

	t.Run("install pending: queued command deleted and RemoveProfile sent", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil, nil, []*automdm.MDMAppleProfilePayload{row(&automdm.MDMDeliveryPending, "install-cmd")})
		require.NoError(t, res.err)

		require.Equal(t, []string{hostUUID}, res.deletedCmds["install-cmd"])
		require.Empty(t, res.cleanedUp, "the row must be converted in place to keep has_acme_payload")
		require.Equal(t, []string{"RemoveProfile"}, res.enqueued)
		require.Len(t, res.upserted, 1)
		require.Equal(t, automdm.MDMOperationTypeRemove, res.upserted[0].OperationType)
		require.True(t, res.upserted[0].IgnoreError)
	})
}

// A profile re-added with a different PayloadScope lands on the other channel, so
// the queued removal on the original channel still has to be delivered: the two are
// separate installs with separate enrollment IDs.
func TestComputeReconcileDeltasScopeChangeKeepsRemoval(t *testing.T) {
	host := &automdm.AppleHostReconcileInfo{
		HostID: 1, UUID: "uuid-A", TeamID: nil, Platform: "darwin",
		LabelUpdatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	readdedAsSystem := &automdm.AppleProfileForReconcile{
		ProfileUUID:       "aNewProfileUUID",
		ProfileIdentifier: "com.example.wifi",
		ProfileName:       "Wi-Fi",
		TeamID:            0,
		Checksum:          []byte("aaaa"),
		Scope:             automdm.PayloadScopeSystem,
		IncludeMode:       automdm.AppleProfileIncludeNone,
	}
	staleUserRemoval := &automdm.MDMAppleProfilePayload{
		ProfileUUID:       "aOldProfileUUID",
		ProfileIdentifier: "com.example.wifi",
		ProfileName:       "Wi-Fi",
		HostUUID:          "uuid-A",
		Checksum:          []byte("aaaa"),
		Scope:             automdm.PayloadScopeUser,
		OperationType:     automdm.MDMOperationTypeRemove,
		Status:            &automdm.MDMDeliveryPending,
		CommandUUID:       "remove-cmd",
	}

	toInstall, toRemove, _ := ComputeReconcileDeltas(
		[]*automdm.AppleHostReconcileInfo{host}, nil,
		map[string][]*automdm.MDMAppleProfilePayload{"uuid-A": {staleUserRemoval}},
		map[uint][]*automdm.AppleProfileForReconcile{0: {readdedAsSystem}},
		map[string]struct{}{}, nil,
	)

	require.Len(t, toInstall, 1)
	require.Equal(t, automdm.PayloadScopeSystem, toInstall[0].Scope)
	require.Empty(t, toRemove, "a user-channel removal must not be cancelled by a system-channel install")
}

// A host moving between teams that both hold a byte-identical profile keeps the
// pending install it already has queued: the new team's row takes over the old
// row's command, so that command must stay in the device's queue.
func TestMDMAppleExecuteReconcileBatchIdenticalProfileTransfer(t *testing.T) {
	const hostUUID = "uuid-A"
	const identifier = "com.example.shared"
	const queuedCmd = "queued-install-cmd"
	varsUpdatedAt := time.Now().Add(-time.Hour)

	newInstall := func(checksum string) *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aTeamBProfileUUID", ProfileIdentifier: identifier, ProfileName: "Shared",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte(checksum),
		}
	}
	oldRow := func(status automdm.MDMDeliveryStatus) *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aTeamAProfileUUID", ProfileIdentifier: identifier, ProfileName: "Shared",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte("aaaa"),
			OperationType: automdm.MDMOperationTypeInstall, Status: &status, CommandUUID: queuedCmd,
			VariablesUpdatedAt: &varsUpdatedAt,
		}
	}

	t.Run("pending install with identical content: queued command kept", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil,
			[]*automdm.MDMAppleProfilePayload{newInstall("aaaa")},
			[]*automdm.MDMAppleProfilePayload{oldRow(automdm.MDMDeliveryPending)})
		require.NoError(t, res.err)

		require.NotContains(t, res.deletedCmds, queuedCmd)
		require.Empty(t, res.enqueued)

		require.Len(t, res.cleanedUp, 1)
		require.Equal(t, "aTeamAProfileUUID", res.cleanedUp[0].ProfileUUID)
		require.Equal(t, "aTeamBProfileUUID", res.cleanedUp[0].AdoptedByProfileUUID, "managed certificates move to the adopter")

		require.Len(t, res.upserted, 1)
		require.Equal(t, "aTeamBProfileUUID", res.upserted[0].ProfileUUID)
		require.Equal(t, queuedCmd, res.upserted[0].CommandUUID)
		require.Equal(t, automdm.MDMDeliveryPending, *res.upserted[0].Status)
		require.Equal(t, &varsUpdatedAt, res.upserted[0].VariablesUpdatedAt)
	})

	t.Run("pending install with different content: queued command replaced", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, nil,
			[]*automdm.MDMAppleProfilePayload{newInstall("bbbb")},
			[]*automdm.MDMAppleProfilePayload{oldRow(automdm.MDMDeliveryPending)})
		require.NoError(t, res.err)

		require.Equal(t, []string{hostUUID}, res.deletedCmds[queuedCmd])
		require.Equal(t, []string{"InstallProfile"}, res.enqueued)

		require.Len(t, res.cleanedUp, 1)
		require.Empty(t, res.cleanedUp[0].AdoptedByProfileUUID, "a reinstall tracks its own certificate")

		require.Len(t, res.upserted, 1)
		require.Equal(t, "aTeamBProfileUUID", res.upserted[0].ProfileUUID)
		require.NotEqual(t, queuedCmd, res.upserted[0].CommandUUID)
	})

	t.Run("host being set up: row reset, so queued command cancelled", func(t *testing.T) {
		res := runReconcileBatchCapturing(t, []string{hostUUID},
			[]*automdm.MDMAppleProfilePayload{newInstall("aaaa")},
			[]*automdm.MDMAppleProfilePayload{oldRow(automdm.MDMDeliveryPending)})
		require.NoError(t, res.err)

		require.Equal(t, []string{hostUUID}, res.deletedCmds[queuedCmd])
		require.Empty(t, res.enqueued)

		require.Len(t, res.upserted, 1)
		require.Nil(t, res.upserted[0].Status)
		require.Empty(t, res.upserted[0].CommandUUID)
	})

	t.Run("enqueue fails: adopted row keeps its queued command", func(t *testing.T) {
		otherInstall := &automdm.MDMAppleProfilePayload{
			ProfileUUID: "aOtherProfileUUID", ProfileIdentifier: "com.example.other", ProfileName: "Other",
			HostUUID: hostUUID, Scope: automdm.PayloadScopeSystem, Checksum: []byte("cccc"),
		}
		res := runReconcileBatchCapturing(t, nil,
			[]*automdm.MDMAppleProfilePayload{newInstall("aaaa"), otherInstall},
			[]*automdm.MDMAppleProfilePayload{oldRow(automdm.MDMDeliveryPending)},
			func(ds *mock.Store) {
				ds.GetGroupedCertificateAuthoritiesFunc = func(ctx context.Context, includeSecrets bool) (*automdm.GroupedCertificateAuthorities, error) {
					return nil, errors.New("enqueue failure")
				}
			})
		require.ErrorContains(t, res.err, "enqueue failure")
		require.NotContains(t, res.deletedCmds, queuedCmd)

		// the rollback mutates the upserted rows in place, so these reflect its result
		byProfile := make(map[string]*automdm.MDMAppleBulkUpsertHostProfilePayload, len(res.upserted))
		for _, hp := range res.upserted {
			byProfile[hp.ProfileUUID] = hp
		}
		require.Len(t, byProfile, 2)

		adopted := byProfile["aTeamBProfileUUID"]
		require.Equal(t, queuedCmd, adopted.CommandUUID)
		require.NotNil(t, adopted.Status)
		require.Equal(t, automdm.MDMDeliveryPending, *adopted.Status)

		reverted := byProfile["aOtherProfileUUID"]
		require.Nil(t, reverted.Status)
		require.Empty(t, reverted.CommandUUID)
	})

	// The device holds the old profile as rendered for its UUID: a pending install would request its certificate from the
	// old proxy URL, and a certificate not recorded with dates yet carries a renewal ID that never matches the new profile.
	t.Run("profile rendered with its own UUID", func(t *testing.T) {
		const renewalID = "<string>CN=$AUTOMDM_VAR_CERTIFICATE_RENEWAL_ID</string>"
		const proxyURL = "<string>$AUTOMDM_VAR_NDES_SCEP_PROXY_URL</string><string>$AUTOMDM_VAR_NDES_SCEP_CHALLENGE</string>"
		recorded := &automdm.MDMManagedCertificate{HostUUID: hostUUID, ProfileUUID: "aTeamAProfileUUID", CAName: "ca1",
			NotValidAfter: new(time.Now().Add(30 * 24 * time.Hour))}
		notRecorded := &automdm.MDMManagedCertificate{HostUUID: hostUUID, ProfileUUID: "aTeamAProfileUUID", CAName: "ca2"}

		for _, tc := range []struct {
			name      string
			contents  string
			status    automdm.MDMDeliveryStatus
			certs     []*automdm.MDMManagedCertificate
			reinstall bool
		}{
			{"renewal ID, verified with its certificate recorded: taken over", renewalID, automdm.MDMDeliveryVerified, []*automdm.MDMManagedCertificate{recorded}, false},
			{"renewal ID, verified, certificate not recorded yet: reinstalled", renewalID, automdm.MDMDeliveryVerified, []*automdm.MDMManagedCertificate{notRecorded}, true},
			{"renewal ID, verified, one of its certificates not recorded: reinstalled", renewalID, automdm.MDMDeliveryVerified, []*automdm.MDMManagedCertificate{recorded, notRecorded}, true},
			{"renewal ID, verified without a certificate row: reinstalled", renewalID, automdm.MDMDeliveryVerified, nil, true},
			{"renewal ID, pending: reinstalled", renewalID, automdm.MDMDeliveryPending, []*automdm.MDMManagedCertificate{recorded}, true},
			// without a renewal ID its certificate rows never get dates, so they are not checked
			{"proxy URL, verified: taken over", proxyURL, automdm.MDMDeliveryVerified, nil, false},
			{"proxy URL, pending: reinstalled", proxyURL, automdm.MDMDeliveryPending, nil, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res := runReconcileBatchCapturing(t, nil,
					[]*automdm.MDMAppleProfilePayload{newInstall("aaaa")},
					[]*automdm.MDMAppleProfilePayload{oldRow(tc.status)},
					func(ds *mock.Store) {
						ds.GetMDMAppleProfilesContentsFunc = func(ctx context.Context, uuids []string) (map[string]mobileconfig.Mobileconfig, error) {
							out := make(map[string]mobileconfig.Mobileconfig, len(uuids))
							for _, u := range uuids {
								out[u] = []byte(tc.contents)
							}
							return out, nil
						}
					},
					func(ds *mock.Store) {
						ds.ListMDMManagedCertificatesForHostProfilesFunc = func(ctx context.Context, hostProfiles []automdm.HostProfileUUID) ([]*automdm.MDMManagedCertificate, error) {
							require.Equal(t, []automdm.HostProfileUUID{{HostUUID: hostUUID, ProfileUUID: "aTeamAProfileUUID"}}, hostProfiles)
							return tc.certs, nil
						}
					})
				require.NoError(t, res.err)
				require.Len(t, res.cleanedUp, 1)
				require.NotEmpty(t, res.upserted)
				install := res.upserted[len(res.upserted)-1]
				require.Equal(t, "aTeamBProfileUUID", install.ProfileUUID)
				if tc.reinstall {
					// the proxy URL row is not enqueued: no NDES CA is configured, so preprocessing fails it
					if tc.contents == renewalID {
						require.Equal(t, []string{"InstallProfile"}, res.enqueued)
					}
					require.NotEqual(t, queuedCmd, install.CommandUUID)
					require.Equal(t, []string{hostUUID}, res.deletedCmds[queuedCmd])
					require.Empty(t, res.cleanedUp[0].AdoptedByProfileUUID, "a reinstall tracks its own certificate")
				} else {
					require.Empty(t, res.enqueued)
					require.Equal(t, queuedCmd, install.CommandUUID)
					require.Equal(t, "aTeamBProfileUUID", res.cleanedUp[0].AdoptedByProfileUUID)
				}
			})
		}
	})
}

func TestProfileUUIDBoundVariables(t *testing.T) {
	for _, tc := range []struct {
		automdmVars         []string
		proxyURL, renewalID bool
	}{
		{nil, false, false},
		{[]string{string(automdm.AutoMDMVarHostEndUserIDPUsername)}, false, false},
		{[]string{string(automdm.AutoMDMVarDigiCertDataPrefix) + "CA"}, false, false},
		{[]string{string(automdm.AutoMDMVarCertificateRenewalID)}, false, true},
		{[]string{string(automdm.AutoMDMVarSCEPRenewalID)}, false, true},
		{[]string{string(automdm.AutoMDMVarNDESSCEPChallenge), string(automdm.AutoMDMVarNDESSCEPProxyURL)}, true, false},
		{[]string{string(automdm.AutoMDMVarCustomSCEPChallengePrefix) + "CA", string(automdm.AutoMDMVarCustomSCEPProxyURLPrefix) + "CA"}, true, false},
		{[]string{string(automdm.AutoMDMVarSmallstepSCEPProxyURLPrefix) + "CA", string(automdm.AutoMDMVarCertificateRenewalID)}, true, true},
	} {
		proxyURL, renewalID := profileUUIDBoundVariables(tc.automdmVars)
		assert.Equal(t, tc.proxyURL, proxyURL, "proxy URL for %v", tc.automdmVars)
		assert.Equal(t, tc.renewalID, renewalID, "renewal ID for %v", tc.automdmVars)
	}
}

// Self-service (opt-in) profiles are desired on a host only while it has an opt-in row, or when it already carries an identical
// same-identifier profile from its previous team (adoption, which records a new opt-in). Opt-ins for anything no longer desired
// as self-service are purged. Force-install -> self-service flips are seeded with opt-ins by the API, so the reconciler never
// has to preserve an un-opted self-service profile on its own.
func TestComputeReconcileDeltasSelfService(t *testing.T) {
	ss := &automdm.AppleProfileForReconcile{
		ProfileUUID: "apple_ss", ProfileIdentifier: "com.example.ss", TeamID: 1,
		Checksum: []byte("ssss"), Scope: automdm.PayloadScopeSystem, SelfService: true,
	}
	variant := func(f func(p *automdm.AppleProfileForReconcile)) *automdm.AppleProfileForReconcile {
		p := *ss
		f(&p)
		return &p
	}
	// The host is always a member of label 50.
	excluded := variant(func(p *automdm.AppleProfileForReconcile) {
		p.ExcludeLabels = []automdm.AppleProfileLabelRef{{LabelID: new(uint(50))}}
	})
	forced := variant(func(p *automdm.AppleProfileForReconcile) { p.SelfService = false })
	forcedExcluded := variant(func(p *automdm.AppleProfileForReconcile) { *p = *excluded; p.SelfService = false })
	broken := variant(func(p *automdm.AppleProfileForReconcile) {
		p.IncludeMode, p.IncludeLabels = automdm.AppleProfileIncludeAll, []automdm.AppleProfileLabelRef{{LabelID: nil}}
	})
	unknownLabel := variant(func(p *automdm.AppleProfileForReconcile) {
		p.IncludeMode = automdm.AppleProfileIncludeAll
		p.IncludeLabels = []automdm.AppleProfileLabelRef{{
			LabelID: new(uint(60)), LabelMembershipType: int(automdm.LabelMembershipTypeDynamic), CreatedAt: time.Now(),
		}}
	})

	row := func(profUUID, checksum string, op automdm.MDMOperationType, status *automdm.MDMDeliveryStatus) *automdm.MDMAppleProfilePayload {
		return &automdm.MDMAppleProfilePayload{
			ProfileUUID: profUUID, ProfileIdentifier: ss.ProfileIdentifier, HostUUID: "uuid-A",
			Checksum: []byte(checksum), Scope: automdm.PayloadScopeSystem, OperationType: op, Status: status,
		}
	}
	install, remove := automdm.MDMOperationTypeInstall, automdm.MDMOperationTypeRemove
	verified := new(automdm.MDMDeliveryVerified)
	oldTeam := row("aOld", "ssss", install, verified) // identical payload from the host's previous team
	userChannel := row("aOld", "ssss", install, verified)
	userChannel.Scope = automdm.PayloadScopeUser

	cases := []struct {
		name     string
		platform string // darwin when empty
		prof     *automdm.AppleProfileForReconcile
		current  *automdm.MDMAppleProfilePayload
		optIns   []string

		wantInstall, wantRemove, wantAdd, wantPurge []string
	}{
		{name: "not opted in -> nothing", prof: ss},
		{name: "opted in -> install", prof: ss, optIns: []string{"apple_ss"}, wantInstall: []string{"apple_ss"}},
		{
			name: "opted in via install endpoint (NULL status) -> install", prof: ss, optIns: []string{"apple_ss"},
			current: row("apple_ss", "ssss", install, nil), wantInstall: []string{"apple_ss"},
		},
		{name: "opted in and verified -> no-op", prof: ss, optIns: []string{"apple_ss"}, current: row("apple_ss", "ssss", install, verified)},
		{name: "installed, opted out -> remove", prof: ss, current: row("apple_ss", "ssss", install, verified), wantRemove: []string{"apple_ss"}},
		{
			name: "uninstall endpoint (NULL status remove) -> remove", prof: ss,
			current: row("apple_ss", "ssss", remove, nil), wantRemove: []string{"apple_ss"},
		},
		{
			name: "opted in, now excluded by label -> remove and purge", prof: excluded, optIns: []string{"apple_ss"},
			current: row("apple_ss", "ssss", install, verified), wantRemove: []string{"apple_ss"}, wantPurge: []string{"apple_ss"},
		},
		{name: "opted in, broken label -> kept", prof: broken, optIns: []string{"apple_ss"}, current: row("apple_ss", "ssss", install, verified)},
		{
			name: "opt-in for a deleted profile -> remove and purge", prof: ss, optIns: []string{"aGone"},
			current: row("aGone", "gone", install, verified), wantRemove: []string{"aGone"}, wantPurge: []string{"aGone"},
		},
		{
			name: "flipped to force install -> kept, purge", prof: forced, optIns: []string{"apple_ss"},
			current: row("apple_ss", "ssss", install, verified), wantPurge: []string{"apple_ss"},
		},
		{
			name: "flipped to force install and excluded -> remove, purge once", prof: forcedExcluded, optIns: []string{"apple_ss"},
			current: row("apple_ss", "ssss", install, verified), wantRemove: []string{"apple_ss"}, wantPurge: []string{"apple_ss"},
		},

		// Team transfers: the install/remove pair on the same identifier is collapsed later by ExecuteReconcileBatch, so only
		// the opt-in delta decides whether the payload stays on the device.
		{
			name: "transfer from force install to identical self-service -> adopt", prof: ss, current: oldTeam,
			wantInstall: []string{"apple_ss"}, wantRemove: []string{"aOld"}, wantAdd: []string{"apple_ss"},
		},
		{
			name: "transfer from opted-in self-service to identical self-service -> adopt, purge old", prof: ss, current: oldTeam,
			optIns: []string{"aOld"}, wantInstall: []string{"apple_ss"}, wantRemove: []string{"aOld"}, wantAdd: []string{"apple_ss"}, wantPurge: []string{"aOld"},
		},
		{
			name: "transfer to self-service with different contents -> no adopt", prof: ss, current: row("aOld", "xxxx", install, verified),
			optIns: []string{"aOld"}, wantRemove: []string{"aOld"}, wantPurge: []string{"aOld"},
		},
		{
			name: "adopt from a pending install", prof: ss, current: row("aOld", "ssss", install, new(automdm.MDMDeliveryPending)),
			wantInstall: []string{"apple_ss"}, wantRemove: []string{"aOld"}, wantAdd: []string{"apple_ss"},
		},
		{
			name: "adopt from a verifying install", prof: ss, current: row("aOld", "ssss", install, new(automdm.MDMDeliveryVerifying)),
			wantInstall: []string{"apple_ss"}, wantRemove: []string{"aOld"}, wantAdd: []string{"apple_ss"},
		},
		{name: "no adopt from an unsent install", prof: ss, current: row("aOld", "ssss", install, nil), wantRemove: []string{"aOld"}},
		{name: "no adopt from a failed install", prof: ss, current: row("aOld", "ssss", install, new(automdm.MDMDeliveryFailed)), wantRemove: []string{"aOld"}},
		{name: "no adopt from a pending removal", prof: ss, current: row("aOld", "ssss", remove, new(automdm.MDMDeliveryPending))},
		{name: "no adopt across channels", prof: ss, current: userChannel, wantRemove: []string{"aOld"}},
		{
			name: "adoption counts as on-host for unknown dynamic label membership", prof: unknownLabel, current: oldTeam,
			wantInstall: []string{"apple_ss"}, wantRemove: []string{"aOld"}, wantAdd: []string{"apple_ss"},
		},

		{name: "ios: opt-in doesn't install, purged", platform: "ios", prof: ss, optIns: []string{"apple_ss"}, wantPurge: []string{"apple_ss"}},
		{name: "ios: no adoption", platform: "ios", prof: ss, current: oldTeam, wantRemove: []string{"aOld"}},
		{name: "opted in but unknown label membership keeps opt-in", prof: unknownLabel, optIns: []string{"apple_ss"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host := &automdm.AppleHostReconcileInfo{
				HostID: 1, UUID: "uuid-A", TeamID: new(uint(1)), Platform: cmp.Or(c.platform, "darwin"),
				LabelUpdatedAt: time.Now().Add(-time.Hour),
			}
			var current map[string][]*automdm.MDMAppleProfilePayload
			if c.current != nil {
				current = map[string][]*automdm.MDMAppleProfilePayload{host.UUID: {c.current}}
			}
			optIns := map[string]map[string]struct{}{host.UUID: {}}
			for _, u := range c.optIns {
				optIns[host.UUID][u] = struct{}{}
			}
			brokenSet := map[string]struct{}{}
			if c.prof.HasBrokenLabel() {
				brokenSet[c.prof.ProfileUUID] = struct{}{}
			}

			toInstall, toRemove, changes := ComputeReconcileDeltas(
				[]*automdm.AppleHostReconcileInfo{host}, map[uint]map[uint]struct{}{host.HostID: {50: {}}}, current,
				map[uint][]*automdm.AppleProfileForReconcile{1: {c.prof}}, brokenSet, optIns,
			)

			profUUIDs := func(ps []*automdm.MDMAppleProfilePayload) (out []string) {
				for _, p := range ps {
					out = append(out, p.ProfileUUID)
				}
				return out
			}
			pairUUIDs := func(ps []automdm.HostProfileUUID) (out []string) {
				for _, p := range ps {
					require.Equal(t, host.UUID, p.HostUUID)
					out = append(out, p.ProfileUUID)
				}
				return out
			}
			assert.ElementsMatch(t, c.wantInstall, profUUIDs(toInstall), "install")
			assert.ElementsMatch(t, c.wantRemove, profUUIDs(toRemove), "remove")
			assert.ElementsMatch(t, c.wantAdd, pairUUIDs(changes.Add), "opt-in add")
			assert.ElementsMatch(t, c.wantPurge, pairUUIDs(changes.Purge), "opt-in purge")
		})
	}
}
