package service

import (
	"context"

	"github.com/Masterminds/semver/v3"
	"github.com/anilyagiz/automdm/v4/server/automdm"
)

// orbitSoftwareQuery is an extra software query backed by an automdmd table whose sources no other
// query reports. While automdmd's extension is not yet registered with osqueryd, as right after a
// reboot, its discovery finds no table and osquery sends nothing for it, which the full replace
// in directIngestSoftware would read as "none installed".
type orbitSoftwareQuery struct {
	sources []string
	// An older automdmd never ran the query, so there is nothing to keep.
	minAutoMDMdVersion *semver.Version
}

// orbitSoftwareQueries lists, by detail query name, the queries whose rows are kept when the
// query did not run. Only the sources a query alone reports are listed: ai_tools rows with its
// pseudo-source are never stored, and the rows it flags on other sources survive on their own.
var orbitSoftwareQueries = map[string]orbitSoftwareQuery{
	"software_linux_automdmd_pacman": {
		sources:            []string{"pacman_packages"},
		minAutoMDMdVersion: semver.MustParse("1.48.1"),
	},
	"software_go_binaries": {
		sources:            []string{"go_binaries"},
		minAutoMDMdVersion: semver.MustParse("1.54.0"),
	},
	"software_adobe_plugins": {
		sources:            []string{"adobe_plugins"},
		minAutoMDMdVersion: semver.MustParse("1.56.2"),
	},
	"software_ai_tools": {
		sources:            []string{"ai_clis", "mcp_servers", "ai_skills"},
		minAutoMDMdVersion: semver.MustParse("1.59.0"),
	},
	"software_linux_automdmd_nix": {
		sources:            []string{"nix_packages"},
		minAutoMDMdVersion: semver.MustParse("1.63.0"),
	},
}

// preservedSoftwareSources returns the sources of the skipped orbit software queries that were
// sent to the host and whose table its automdmd ships. The automdmd version is read from
// host_orbit_info because orbit_info is itself an orbit table and misses the same batches.
func (svc *Service) preservedSoftwareSources(ctx context.Context, host *automdm.Host, cfg *hostDetailQueryConfig, skipped []string) []string {
	var candidates []string
	for _, name := range skipped {
		if _, ok := orbitSoftwareQueries[name]; !ok {
			continue
		}
		if dq, sent := cfg.detailQueries[name]; sent && dq.RunsForPlatform(host.Platform) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// A read failure keeps the rows: keeping is a no-op on a host that lacks the table.
	var automdmdVersion *semver.Version
	automdmdVersionString := "unknown"
	info, err := svc.ds.GetHostOrbitInfo(ctx, host.ID)
	switch {
	case automdm.IsNotFound(err):
		return nil
	case err != nil:
		svc.logger.ErrorContext(ctx, "reading host orbit info", "host_id", host.ID, "err", err)
	default:
		automdmdVersion, err = automdm.VersionToSemverVersion(info.Version)
		if err != nil {
			svc.logger.DebugContext(ctx, "unparseable automdmd version", "host_id", host.ID, "version", info.Version)
			return nil
		}
		automdmdVersionString = info.Version
	}

	var kept, sources []string
	for _, name := range candidates {
		oq := orbitSoftwareQueries[name]
		if automdmdVersion != nil && automdmdVersion.LessThan(oq.minAutoMDMdVersion) {
			continue
		}
		kept = append(kept, name)
		sources = append(sources, oq.sources...)
	}
	if len(kept) > 0 {
		svc.logger.InfoContext(ctx, "orbit software queries did not run, keeping previously reported rows",
			"queries", kept,
			"sources", sources,
			"automdmd_version", automdmdVersionString,
			"host_id", host.ID,
		)
	}
	return sources
}

// mainSoftwareQueries receive the merged extra query rows and reach directIngestSoftware. Only
// one of them runs on a host.
var mainSoftwareQueries = []string{
	hostDetailQueryPrefix + "software_macos",
	hostDetailQueryPrefix + "software_windows",
	hostDetailQueryPrefix + "software_linux",
}

func hasMainSoftwareResults(results automdm.OsqueryDistributedQueryResults) bool {
	for _, query := range mainSoftwareQueries {
		if _, ok := results[query]; ok {
			return true
		}
	}
	return false
}
