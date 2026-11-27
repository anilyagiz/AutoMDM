package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	ma "github.com/anilyagiz/automdm/v4/ee/maintained-apps"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/datastore/mysql/mysqltest"
	"github.com/anilyagiz/automdm/v4/server/dev_mode"
	"github.com/anilyagiz/automdm/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A package replaced on one team keeps the URL and ETag of the original
// download. Another team adding that URL must still end up with the origin's
// bytes.
func (s *integrationEnterpriseTestSuite) TestBatchSetSoftwareInstallersConditionalDownloadAfterPackageReplace() {
	t := s.T()
	ctx := context.Background()

	originBytes, err := os.ReadFile(filepath.Join("testdata", "software-installers", "ruby.deb"))
	require.NoError(t, err)
	originHash := sha256Hex(originBytes)
	// Same package name and extension, different bytes.
	replacedBytes := append(bytes.Clone(originBytes), []byte("replaced")...)
	replacedHash := sha256Hex(replacedBytes)
	require.NotEqual(t, originHash, replacedHash)

	const originETag = `"ruby-deb-origin"`
	var (
		mu           sync.Mutex
		ifNoneMatchs []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ifNoneMatchs = append(ifNoneMatchs, r.Header.Get("If-None-Match"))
		mu.Unlock()
		w.Header().Set("ETag", originETag)
		if r.Header.Get("If-None-Match") == originETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
		_, err := w.Write(originBytes)
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)
	pkgURL := srv.URL + "/ruby.deb"
	lastIfNoneMatch := func() string {
		mu.Lock()
		defer mu.Unlock()
		require.NotEmpty(t, ifNoneMatchs)
		return ifNoneMatchs[len(ifNoneMatchs)-1]
	}
	requestCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(ifNoneMatchs)
	}

	newAutoMDM := func(suffix string) *automdm.Team {
		tm, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + suffix})
		require.NoError(t, err)
		return tm
	}
	automdmA := newAutoMDM("-a")
	automdmEarly := newAutoMDM("-early")
	automdmB := newAutoMDM("-b")
	automdmPinned := newAutoMDM("-pinned")
	automdmAlways := newAutoMDM("-always")

	batch := func(tm *automdm.Team, payloads []*automdm.SoftwareInstallerPayload) []automdm.SoftwarePackageResponse {
		t.Helper()
		var resp batchSetSoftwareInstallersResponse
		s.DoJSON("POST", "/api/latest/automdm/software/batch",
			batchSetSoftwareInstallersRequest{Software: payloads},
			http.StatusAccepted, &resp, "team_name", tm.Name)
		return waitBatchSetSoftwareInstallersCompleted(t, &s.withServer, tm.Name, resp.RequestUUID)
	}
	addURL := func(tm *automdm.Team, p automdm.SoftwareInstallerPayload) {
		t.Helper()
		p.URL = pkgURL
		pkgs := batch(tm, []*automdm.SoftwareInstallerPayload{&p})
		require.Len(t, pkgs, 1)
	}
	// storedHash hashes the bytes AutoMDM serves for the team's installer.
	storedHash := func(tm *automdm.Team, titleID uint) string {
		t.Helper()
		r := s.Do("GET", fmt.Sprintf("/api/latest/automdm/software/titles/%d/package?alt=media", titleID), nil,
			http.StatusOK, "team_id", fmt.Sprint(tm.ID))
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		return sha256Hex(b)
	}
	installerRow := func(tm *automdm.Team) (storageID, url, etag string) {
		t.Helper()
		var row struct {
			StorageID string  `db:"storage_id"`
			URL       string  `db:"url"`
			ETag      *string `db:"http_etag"`
		}
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			return sqlx.GetContext(ctx, q, &row,
				`SELECT storage_id, url, http_etag FROM software_installers WHERE global_or_team_id = ?`, tm.ID)
		})
		if row.ETag != nil {
			etag = *row.ETag
		}
		return row.StorageID, row.URL, etag
	}

	// Maintainer on team A only.
	maintainerA := &automdm.User{
		Name:  "AutoMDM A maintainer",
		Email: uuid.NewString() + "@example.com",
		Teams: []automdm.UserTeam{{Team: *automdmA, Role: automdm.RoleMaintainer}},
	}
	require.NoError(t, maintainerA.SetPassword(test.GoodPassword, 10, 10))
	_, err = s.ds.NewUser(ctx, maintainerA)
	require.NoError(t, err)
	adminToken := s.token
	t.Cleanup(func() { s.token = s.getTestAdminToken() })

	// 1. AutoMDM A adds the URL and stores the origin's bytes and ETag.
	s.setTokenForTest(t, maintainerA.Email, test.GoodPassword)
	addURL(automdmA, automdm.SoftwareInstallerPayload{})
	storageA, urlA, etagA := installerRow(automdmA)
	require.Equal(t, originHash, storageA)
	require.Equal(t, pkgURL, urlA)
	require.Equal(t, originETag, etagA)
	titleID := getSoftwareTitleID(t, s.ds, "ruby", "deb_packages")

	// Before the replace, another team adding the URL gets the origin's bytes.
	s.token = adminToken
	addURL(automdmEarly, automdm.SoftwareInstallerPayload{})
	require.Equal(t, originETag, lastIfNoneMatch())
	require.Equal(t, originHash, storedHash(automdmEarly, titleID))

	// 2. AutoMDM A replaces the package. The row keeps its URL but drops the ETag,
	// which described the origin's bytes.
	s.setTokenForTest(t, maintainerA.Email, test.GoodPassword)
	tfr, err := automdm.NewTempFileReader(bytes.NewReader(replacedBytes), t.TempDir)
	require.NoError(t, err)
	defer tfr.Close()
	s.updateSoftwareInstaller(t, &automdm.UpdateSoftwareInstallerPayload{
		Filename:      "ruby.deb",
		InstallerFile: tfr,
		TitleID:       titleID,
		TeamID:        &automdmA.ID,
	}, http.StatusOK, "")
	storageA, urlA, etagA = installerRow(automdmA)
	require.Equal(t, replacedHash, storageA)
	require.Equal(t, pkgURL, urlA)
	require.Empty(t, etagA)

	// AutoMDM A's maintainer can't add software to team B.
	var forbidden batchSetSoftwareInstallersResponse
	s.DoJSON("POST", "/api/latest/automdm/software/batch",
		batchSetSoftwareInstallersRequest{Software: []*automdm.SoftwareInstallerPayload{{URL: pkgURL}}},
		http.StatusForbidden, &forbidden, "team_name", automdmB.Name)

	// Remove the earlier team's row so team A's is the only match for the URL.
	s.token = adminToken
	batch(automdmEarly, []*automdm.SoftwareInstallerPayload{})

	// 3 and 4. AutoMDM B adds the URL with the defaults: no hash, no always_download.
	// AutoMDM A's row has no ETag, so AutoMDM downloads the whole file.
	addURL(automdmB, automdm.SoftwareInstallerPayload{})
	require.Empty(t, lastIfNoneMatch())
	storageB, _, _ := installerRow(automdmB)
	assert.Equal(t, originHash, storageB, "team B must store the origin's bytes, got team A's replaced package")
	assert.Equal(t, originHash, storedHash(automdmB, titleID), "team B must serve the origin's bytes")

	// A pinned hash of the origin's bytes gets the origin's bytes.
	addURL(automdmPinned, automdm.SoftwareInstallerPayload{SHA256: originHash})
	require.Equal(t, originHash, storedHash(automdmPinned, titleID))

	// always_download skips the conditional request.
	before := requestCount()
	addURL(automdmAlways, automdm.SoftwareInstallerPayload{AlwaysDownload: true})
	require.Equal(t, before+1, requestCount())
	require.Empty(t, lastIfNoneMatch())
	require.Equal(t, originHash, storedHash(automdmAlways, titleID))
}

// Same as above for an AutoMDM-maintained app whose manifest has no hash and
// whose installer URL matches a replaced custom package on another team.
func (s *integrationEnterpriseTestSuite) TestBatchSetSoftwareInstallersConditionalDownloadAfterPackageReplaceFMA() {
	t := s.T()
	ctx := context.Background()

	originBytes, err := os.ReadFile(filepath.Join("testdata", "software-installers", "dummy_installer.pkg"))
	require.NoError(t, err)
	originHash := sha256Hex(originBytes)
	replacedBytes := append(bytes.Clone(originBytes), []byte("replaced")...)
	replacedHash := sha256Hex(replacedBytes)

	const originETag = `"fma-pkg-origin"`
	var (
		mu           sync.Mutex
		ifNoneMatchs []string
	)
	installerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ifNoneMatchs = append(ifNoneMatchs, r.Header.Get("If-None-Match"))
		mu.Unlock()
		w.Header().Set("ETag", originETag)
		if r.Header.Get("If-None-Match") == originETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, err := w.Write(originBytes)
		assert.NoError(t, err)
	}))
	t.Cleanup(installerSrv.Close)
	pkgURL := installerSrv.URL + "/app.pkg"

	manifestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(ma.FMAManifestFile{Versions: []*ma.FMAManifestApp{{
			Version:      "6.0",
			Queries:      ma.FMAQueries{Exists: "SELECT 1 FROM osquery_info;"},
			InstallerURL: pkgURL,
			SHA256:       "no_check",
		}}})
		assert.NoError(t, err)
	}))
	t.Cleanup(manifestSrv.Close)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_BASE_URL", manifestSrv.URL, t)
	dev_mode.SetOverride("AUTOMDM_DEV_MAINTAINED_APPS_FALLBACK_BASE_URL", manifestSrv.URL, t)
	fma, err := s.ds.UpsertMaintainedApp(ctx, &automdm.MaintainedApp{
		Name:             "Conditional download app",
		Slug:             "conditional-download-app/darwin",
		Platform:         "darwin",
		UniqueIdentifier: "com.example.conditional-download-app",
	})
	require.NoError(t, err)

	automdmA, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + "-a"})
	require.NoError(t, err)
	automdmB, err := s.ds.NewTeam(ctx, &automdm.Team{Name: t.Name() + "-b"})
	require.NoError(t, err)
	maintainerA := &automdm.User{
		Name:  "AutoMDM A maintainer",
		Email: uuid.NewString() + "@example.com",
		Teams: []automdm.UserTeam{{Team: *automdmA, Role: automdm.RoleMaintainer}},
	}
	require.NoError(t, maintainerA.SetPassword(test.GoodPassword, 10, 10))
	_, err = s.ds.NewUser(ctx, maintainerA)
	require.NoError(t, err)
	adminToken := s.token
	t.Cleanup(func() { s.token = s.getTestAdminToken() })

	batch := func(tm *automdm.Team, p *automdm.SoftwareInstallerPayload) automdm.SoftwarePackageResponse {
		t.Helper()
		var resp batchSetSoftwareInstallersResponse
		s.DoJSON("POST", "/api/latest/automdm/software/batch",
			batchSetSoftwareInstallersRequest{Software: []*automdm.SoftwareInstallerPayload{p}},
			http.StatusAccepted, &resp, "team_name", tm.Name)
		pkgs := waitBatchSetSoftwareInstallersCompleted(t, &s.withServer, tm.Name, resp.RequestUUID)
		require.Len(t, pkgs, 1)
		return pkgs[0]
	}
	storageID := func(tm *automdm.Team) string {
		var id string
		mysqltest.ExecAdhocSQL(t, s.ds, func(q sqlx.ExtContext) error {
			return sqlx.GetContext(ctx, q, &id, `SELECT storage_id FROM software_installers WHERE global_or_team_id = ?`, tm.ID)
		})
		return id
	}

	// AutoMDM A adds the FMA's installer URL as a custom package, then replaces it.
	s.setTokenForTest(t, maintainerA.Email, test.GoodPassword)
	pkgA := batch(automdmA, &automdm.SoftwareInstallerPayload{URL: pkgURL})
	require.Equal(t, originHash, storageID(automdmA))
	require.NotNil(t, pkgA.TitleID)
	tfr, err := automdm.NewTempFileReader(bytes.NewReader(replacedBytes), t.TempDir)
	require.NoError(t, err)
	defer tfr.Close()
	s.updateSoftwareInstaller(t, &automdm.UpdateSoftwareInstallerPayload{
		Filename:      "app.pkg",
		InstallerFile: tfr,
		TitleID:       *pkgA.TitleID,
		TeamID:        &automdmA.ID,
	}, http.StatusOK, "")
	require.Equal(t, replacedHash, storageID(automdmA))

	// AutoMDM B adds the AutoMDM-maintained app.
	s.token = adminToken
	batch(automdmB, &automdm.SoftwareInstallerPayload{Slug: &fma.Slug})
	mu.Lock()
	require.NotEmpty(t, ifNoneMatchs)
	last := ifNoneMatchs[len(ifNoneMatchs)-1]
	mu.Unlock()
	require.Empty(t, last)
	assert.Equal(t, originHash, storageID(automdmB), "team B's AutoMDM-maintained app must store the origin's bytes, got team A's replaced package")
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
