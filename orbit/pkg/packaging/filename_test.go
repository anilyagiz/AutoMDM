package packaging

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputFilename(t *testing.T) {
	for _, tc := range []struct {
		platform, arch, ext string
		withTeam, noAutoMDM string
	}{
		{"macos", "", "pkg", "automdm-macos-workstations.pkg", "automdm-macos.pkg"},
		{"windows", "amd64", "msi", "automdm-windows-amd64-workstations.msi", "automdm-windows-amd64.msi"},
		{"windows", "arm64", "msi", "automdm-windows-arm64-workstations.msi", "automdm-windows-arm64.msi"},
		{"linux", "amd64", "deb", "automdm-linux-amd64-workstations.deb", "automdm-linux-amd64.deb"},
		{"linux", "arm64", "deb", "automdm-linux-arm64-workstations.deb", "automdm-linux-arm64.deb"},
		{"linux", "amd64", "rpm", "automdm-linux-amd64-workstations.rpm", "automdm-linux-amd64.rpm"},
		{"linux", "arm64", "rpm", "automdm-linux-arm64-workstations.rpm", "automdm-linux-arm64.rpm"},
		{"linux", "amd64", "pkg.tar.zst", "automdm-linux-amd64-workstations.pkg.tar.zst", "automdm-linux-amd64.pkg.tar.zst"},
		{"linux", "arm64", "pkg.tar.zst", "automdm-linux-arm64-workstations.pkg.tar.zst", "automdm-linux-arm64.pkg.tar.zst"},
	} {
		assert.Equal(t, tc.withTeam, outputFilename(tc.platform, tc.arch, tc.ext, Options{AutoMDMName: "Workstations"}))
		// A name that cleans up to nothing never leaves a trailing dash (automdm-windows-amd64-.msi), even when another
		// team's name is just as blank.
		for _, blank := range []string{"", "\U0001F680 \U0001F680", "$$$ ---", "\u202e\u200d"} {
			assert.Equal(t, tc.noAutoMDM, outputFilename(tc.platform, tc.arch, tc.ext, Options{AutoMDMName: blank}), blank)
			assert.Equal(t, tc.noAutoMDM, outputFilename(tc.platform, tc.arch, tc.ext,
				Options{AutoMDMName: blank, AutoMDMID: 7, OtherTeams: []AutoMDM{{ID: 8, Name: "\U0001F6F8"}}}), blank)
		}
	}

	t.Run("long names are cut at a character boundary", func(t *testing.T) {
		for _, opt := range []Options{
			{AutoMDMName: strings.Repeat("a", 255)},
			{AutoMDMName: strings.Repeat("東", 255)},
			{AutoMDMName: strings.Repeat("ab-", 100)},
			{AutoMDMName: strings.Repeat("東", 255), AutoMDMID: 4294967295, OtherTeams: []AutoMDM{{ID: 1, Name: strings.Repeat("東", 254)}}},
		} {
			for _, ext := range []string{"pkg.tar.zst", "msi"} {
				got := outputFilename("linux", "arm64", ext, opt)
				assert.LessOrEqual(t, len(got), maxFilenameBytes, got)
				assert.True(t, utf8.ValidString(got), got)
				assert.True(t, strings.HasPrefix(got, "automdm-linux-arm64-"), got)
				assert.True(t, strings.HasSuffix(got, "."+ext), got)
				assert.NotContains(t, got, "-.", got)
			}
		}
		assert.Len(t, outputFilename("windows", "amd64", "msi", Options{AutoMDMName: strings.Repeat("a", 255)}), maxFilenameBytes)
	})
}

func TestAutoMDMFilenamePart(t *testing.T) {
	long := strings.Repeat("a", 80)
	for _, tc := range []struct {
		name    string
		automdm string
		id      uint
		others  []AutoMDM
		want    string
	}{
		{"no other teams", "Workstations", 12, nil, "workstations"},
		{"other teams with different names", "Workstations", 12, []AutoMDM{{1, "Servers"}, {2, "Workstations 2"}}, "workstations"},
		{"other team differs only in case and emojis", "Workstations", 12, []AutoMDM{{1, "\U0001F4BB WORKSTATIONS"}}, "workstations-12"},
		{"other team differs only in punctuation", "Workstations (Canary)", 7, []AutoMDM{{1, "workstations-canary"}}, "workstations-canary-7"},
		{"other team differs only by a long s", "Workstations", 4, []AutoMDM{{1, "Work\u017ftations"}}, "workstations-4"},
		{"other team differs only by full case folding", "Stra\u00dfe", 4, []AutoMDM{{5, "Strasse"}}, "stra\u00dfe-4"},
		{"other team differs only by a ligature", "\ufb01nance", 6, []AutoMDM{{1, "Finance"}}, "\ufb01nance-6"},
		{"invisible joiner doesn't dodge the ID", "Workstations\u034f!", 6, []AutoMDM{{1, "Workstations"}}, "workstations-6"},
		{"names that match only after the cut", long + "x", 3, []AutoMDM{{1, long + "y"}}, strings.Repeat("a", 28) + "-3"},
		{"name equal to another team's name with its ID", "foo-1", 3, []AutoMDM{{1, "foo"}, {2, "FOO"}}, "foo-1-3"},
		{"name with a number next to a team that keeps its name", "Workstations 2", 5, []AutoMDM{{1, "Workstations"}}, "workstations-2"},
		{"no ID to add", "Workstations", 0, []AutoMDM{{1, "workstations"}}, "workstations"},
		{"emoji-only names stay without a team name", "\U0001F680", 5, []AutoMDM{{1, "\U0001F6F8"}}, ""},
		// U+043E is a Cyrillic "о": it looks like the Latin "o" but is a different letter, so no collision.
		{"lookalike Cyrillic letter", "W\u043erkstations", 9, []AutoMDM{{1, "Workstations"}}, "w\u043erkstations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, automdmFilenamePart(AutoMDM{ID: tc.id, Name: tc.automdm}, tc.others, 30))
		})
	}

	t.Run("every team gets its own part, whatever the order", func(t *testing.T) {
		teams := []AutoMDM{{1, "foo"}, {2, "FOO"}, {3, "foo-1"}, {9, "foo-1-3"}, {4, "Workstations"}, {5, "Workstations 2"}}
		want := map[uint]string{1: "foo-1", 2: "foo-2", 3: "foo-1-3", 9: "foo-1-3-9", 4: "workstations", 5: "workstations-2"}
		for i, f := range teams {
			others := append(slices.Clone(teams[:i]), teams[i+1:]...)
			require.Equal(t, want[f.ID], automdmFilenamePart(f, others, 30), f.Name)
			slices.Reverse(others)
			require.Equal(t, want[f.ID], automdmFilenamePart(f, others, 30), f.Name)
		}
	})

	t.Run("no room left after the cut", func(t *testing.T) {
		require.Empty(t, automdmFilenamePart(AutoMDM{Name: "Workstations"}, nil, 0))
		// Too little room for the name next to the ID: only the ID, with no dash in front.
		require.Equal(t, "12", automdmFilenamePart(AutoMDM{ID: 12, Name: "Workstations"}, []AutoMDM{{1, "workstations"}}, 3))
	})
}

func TestTeamNameForFilename(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"simple", "Workstations", "workstations"},
		{"spaces and punctuation collapse", "  Workstations -- (Canary)  ", "workstations-canary"},
		{"path separators and dots", `../../tmp/x\y`, "tmp-x-y"},
		{"windows-reserved characters", `a:b*c?d"e<f>g|h`, "a-b-c-d-e-f-g-h"},
		{"control characters", "a\tb\nc\x00d", "a-b-c-d"},
		{"emojis between words", "Mac\U0001F4BBLaptops", "mac-laptops"},
		{"emoji with variation selector and joiner", "\u2764\ufe0f Team \U0001F469\u200d\U0001F4BB", "team"},
		{"keycap digit", "Floor 1\ufe0f\u20e3", "floor-1"},
		{"only emojis", "\U0001F680\U0001F680", ""},
		{"only symbols", "$$$ ---", ""},
		{"empty", "", ""},
		{"accents", "Équipe Été", "équipe-été"},
		{"decomposed accents are composed", "Équipe", "équipe"},
		{"CJK", "東京 チーム", "東京-チーム"},
		{"combining marks in Devanagari", "हिन्दी टीम", "हिन्दी-टीम"},
		{"combining mark after a separator", "a \u0301b", "a-b"},
		{"only combining marks", "́̂", ""},
		{"zero-width non-joiner inside a word", "می\u200cخواهم", "میخواهم"},
		{"soft hyphen", "Work\u00adstations", "workstations"},
		{"right-to-left override", "abc\u202egpj.exe", "abcgpj-exe"},
		{"invisible letters and marks", "Work\u034fstations\u3164\u17b4", "workstations"},
		{"accent split from its letter by a zero-width character", "Cafe\u200c\u0301", "caf\u00e9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, teamNameForFilename(tc.in))
		})
	}
}
