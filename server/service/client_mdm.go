package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/anilyagiz/automdm/v4/pkg/file"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	platform_http "github.com/anilyagiz/automdm/v4/server/platform/http"
	"github.com/beevik/etree"
	"github.com/google/uuid"
	"howett.net/plist"
)

// GetAppleMDM retrieves the Apple MDM APNs information.
func (c *Client) GetAppleMDM() (*automdm.AppleMDM, error) {
	verb, path := "GET", "/api/latest/automdm/apns"
	var responseBody getAppleMDMResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, "")
	return responseBody.AppleMDM, err
}

// GetVPPTokens retrieves the List Volume Purchasing Program (VPP) tokens
func (c *Client) GetVPPTokens() ([]*automdm.VPPTokenDB, error) {
	verb, path := "GET", "/api/latest/automdm/vpp_tokens"
	var responseBody getVPPTokensResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, "")
	return responseBody.Tokens, err
}

func (c *Client) CountABMTokens() (int, error) {
	verb, path := "GET", "/api/latest/automdm/ab_tokens/count"
	var responseBody countABMTokensResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, "")
	return responseBody.Count, err
}

// ListABMTokens retrieves the ABM tokens with their default team assignments.
func (c *Client) ListABTokens() ([]*automdm.ABMToken, error) {
	verb, path := "GET", "/api/latest/automdm/ab_tokens"
	var responseBody listABMTokensResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, "")
	return responseBody.Tokens, err
}

// CountHosts returns the number of hosts matching the given raw query string
// (e.g. "team_id=1&mdm_enrollment_status=pending").
func (c *Client) CountHosts(query string) (int, error) {
	verb, path := "GET", "/api/latest/automdm/hosts/count"
	var responseBody countHostsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query)
	return responseBody.Count, err
}

// RequestAppleCSR requests a signed CSR from the AutoMDM server and returns the
// CSR bytes
func (c *Client) RequestAppleCSR() ([]byte, error) {
	verb, path := "GET", "/api/latest/automdm/mdm/apple/request_csr"
	var resp getMDMAppleCSRResponse
	err := c.authenticatedRequest(nil, verb, path, &resp)
	return resp.CSR, err
}

// RequestAppleAB requests a signed CSR from the AutoMDM server and returns the
// public key bytes
func (c *Client) RequestAppleAB() ([]byte, error) {
	verb, path := "GET", "/api/latest/automdm/mdm/apple/ab_public_key"
	var resp generateABMKeyPairResponse
	err := c.authenticatedRequest(nil, verb, path, &resp)
	return resp.PublicKey, err
}

func (c *Client) GetBootstrapPackageMetadata(teamID uint, forUpdate bool) (*automdm.MDMAppleBootstrapPackage, error) {
	verb, path := "GET", fmt.Sprintf("/api/latest/automdm/bootstrap/%d/metadata", teamID)
	var responseBody bootstrapPackageMetadataResponse
	var err error
	if forUpdate {
		err = c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, "for_update=true")
	} else {
		err = c.authenticatedRequest(nil, verb, path, &responseBody)
	}
	return responseBody.MDMAppleBootstrapPackage, err
}

func (c *Client) DeleteBootstrapPackageIfNeeded(teamID uint, dryRun bool) error {
	_, err := c.GetBootstrapPackageMetadata(teamID, true)
	switch {
	case isNotFoundErr(err):
		// not found is OK, it means there is nothing to delete
		return nil
	case err != nil:
		return fmt.Errorf("getting bootstrap package metadata: %w", err)
	}

	err = c.DeleteBootstrapPackage(teamID, dryRun)
	if err != nil {
		return fmt.Errorf("deleting bootstrap package: %w", err)
	}
	return nil
}

func (c *Client) DeleteBootstrapPackage(teamID uint, dryRun bool) error {
	verb, path := "DELETE", fmt.Sprintf("/api/latest/automdm/bootstrap/%d", teamID)
	var responseBody deleteBootstrapPackageResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, fmt.Sprintf("dry_run=%t", dryRun))
	return err
}

func (c *Client) UploadBootstrapPackage(pkg *automdm.MDMAppleBootstrapPackage, dryRun bool) error {
	uploadID, err := c.stageBootstrapPackage(pkg)
	if err != nil {
		return err
	}
	return c.registerBootstrapPackage(pkg, uploadID, dryRun)
}

// stageBootstrapPackage sends the package bytes straight to the object store, so
// they don't cross AutoMDM's ingress, when the server supports it. It returns an
// empty upload id otherwise.
func (c *Client) stageBootstrapPackage(pkg *automdm.MDMAppleBootstrapPackage) (string, error) {
	appCfg, err := c.GetAppConfig()
	if err != nil {
		return "", fmt.Errorf("getting app config: %w", err)
	}
	if !appCfg.StagedUploadAvailable {
		return "", nil
	}
	return c.stagedUpload(automdm.StagedUploadTargetBootstrapPackage, pkg.TeamID, pkg.Bytes)
}

// registerBootstrapPackage adds the package from uploadID, or from pkg.Bytes when uploadID is empty.
func (c *Client) registerBootstrapPackage(pkg *automdm.MDMAppleBootstrapPackage, uploadID string, dryRun bool) error {
	verb, path := "POST", "/api/latest/automdm/bootstrap"

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	if uploadID != "" {
		if err := w.WriteField("upload_id", uploadID); err != nil {
			return err
		}
		if err := w.WriteField("filename", pkg.Name); err != nil {
			return err
		}
	} else {
		// add the package field
		fw, err := w.CreateFormFile("package", pkg.Name)
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, bytes.NewBuffer(pkg.Bytes)); err != nil {
			return err
		}
	}

	// add the team_id field
	if err := w.WriteField("team_id", fmt.Sprint(pkg.TeamID)); err != nil {
		return err
	}

	w.Close()

	response, err := c.doContextWithBodyAndHeaders(
		context.Background(), verb, path, fmt.Sprintf("dry_run=%t", dryRun),
		b.Bytes(),
		map[string]string{
			"Content-Type":  w.FormDataContentType(),
			"Accept":        "application/json",
			"Authorization": fmt.Sprintf("Bearer %s", c.token),
		},
	)
	if err != nil {
		return fmt.Errorf("do multipart request: %w", err)
	}
	defer response.Body.Close()

	var bpResponse uploadBootstrapPackageResponse
	if err := c.ParseResponse(verb, path, response, &bpResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	return nil
}

// stagedUpload uploads content to a presigned object store URL and returns the
// upload id to register it with.
func (c *Client) stagedUpload(target automdm.StagedUploadTarget, teamID uint, content []byte) (string, error) {
	var staged createStagedUploadResponse
	if err := c.authenticatedRequest(createStagedUploadRequest{Target: target, AutoMDMID: teamID, Size: int64(len(content))},
		"POST", "/api/latest/automdm/staged_upload", &staged); err != nil {
		return "", fmt.Errorf("creating staged upload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, staged.URL, bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("creating staged upload request: %w", err)
	}
	// No AutoMDM Authorization header: the URL is presigned for the object store.
	resp, err := automdmhttp.NewClient(automdmhttp.WithTimeout(automdm.StagedUploadURLExpiry)).Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading to staged upload URL: %w", platform_http.MaskURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("uploading to staged upload URL: status %d: %s", resp.StatusCode, body)
	}
	return staged.UploadID, nil
}

func (c *Client) UploadBootstrapPackageIfNeeded(bp *automdm.MDMAppleBootstrapPackage, teamID uint, dryRun bool) error {
	isFirstTime := false
	oldMeta, err := c.GetBootstrapPackageMetadata(teamID, true)
	if err != nil {
		// not found is OK, it means this is our first time uploading a package
		if !isNotFoundErr(err) {
			return fmt.Errorf("getting bootstrap package metadata: %w", err)
		}
		isFirstTime = true
	}

	if !isFirstTime {
		// compare checksums, if they're equal then we can skip the package upload.
		if bytes.Equal(oldMeta.Sha256, bp.Sha256) {
			return nil
		}
	}

	// The server replaces an existing package only once the new one is saved, so a failed upload
	// leaves the old one in place.
	bp.TeamID = teamID
	uploadID, err := c.stageBootstrapPackage(bp)
	if err != nil {
		return err
	}
	return c.registerBootstrapPackage(bp, uploadID, dryRun)
}

func (c *Client) ValidateBootstrapPackageFromURL(url string) (*automdm.MDMAppleBootstrapPackage, error) {
	if err := c.CheckPremiumMDMEnabled(); err != nil {
		return nil, err
	}

	return downloadRemoteMacosBootstrapPackage(url)
}

func downloadRemoteMacosBootstrapPackage(pkgURL string) (*automdm.MDMAppleBootstrapPackage, error) {
	resp, err := http.Get(pkgURL) // nolint:gosec // we want this URL to be provided by the user. It will run on their machine.
	if err != nil {
		return nil, fmt.Errorf("downloading bootstrap package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("the URL to the macos_bootstrap_package doesn't exist. Please make this URL publicly accessible to the internet.")
	}

	// try to extract the name from a header
	var filename string
	cdh, ok := resp.Header["Content-Disposition"]
	if ok && len(cdh) > 0 {
		_, params, err := mime.ParseMediaType(cdh[0])
		if err == nil {
			filename = params["filename"]
		}
	}

	// if it fails, try to extract it from the URL
	if filename == "" {
		filename = file.ExtractFilenameFromURLPath(pkgURL, "pkg")
	}

	// if all else fails, use a default name
	if filename == "" {
		filename = "bootstrap-package.pkg"
	}

	// get checksums
	var pkgBuf bytes.Buffer
	hash := sha256.New()
	if _, err := io.Copy(hash, io.TeeReader(resp.Body, &pkgBuf)); err != nil {
		return nil, fmt.Errorf("calculating sha256 of package: %w", err)
	}

	pkgReader := bytes.NewReader(pkgBuf.Bytes())
	if err := file.CheckPKGSignature(pkgReader); err != nil {
		switch {
		case errors.Is(err, file.ErrInvalidType):
			return nil, errors.New("Couldn’t edit macos_bootstrap_package. The file must be a package (.pkg).")
		case errors.Is(err, file.ErrNotSigned):
			return nil, errors.New("Couldn’t edit macos_bootstrap_package. The macos_bootstrap_package must be signed. Learn how to sign the package in the AutoMDM documentation: https://automdm.com/learn-more-about/setup-experience/bootstrap-package")
		default:
			return nil, fmt.Errorf("checking package signature: %w", err)
		}
	}

	return &automdm.MDMAppleBootstrapPackage{
		Name:   filename,
		Bytes:  pkgBuf.Bytes(),
		Sha256: hash.Sum(nil),
	}, nil
}

func (c *Client) validateMacOSSetupAssistant(fileName string) ([]byte, error) {
	if err := c.CheckAppleMDMEnabled(); err != nil {
		return nil, err
	}

	if strings.ToLower(filepath.Ext(fileName)) != ".json" {
		return nil, errors.New("Couldn’t edit apple_setup_assistant. The file should be a .json file.")
	}

	b, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("Couldn’t edit apple_setup_assistant. The file should include valid JSON: %w", err)
	}

	return b, nil
}

func (c *Client) uploadMacOSSetupAssistant(data []byte, teamID *uint, name string) error {
	verb, path := http.MethodPost, "/api/latest/automdm/enrollment_profiles/automatic"
	request := createMDMAppleSetupAssistantRequest{
		TeamID:            teamID,
		Name:              name,
		EnrollmentProfile: json.RawMessage(data),
	}
	return c.authenticatedRequest(request, verb, path, nil)
}

func (c *Client) deleteMacOSSetupAssistant(teamID *uint) error {
	verb, path := http.MethodDelete, "/api/latest/automdm/enrollment_profiles/automatic"
	request := deleteMDMAppleSetupAssistantRequest{
		TeamID: teamID,
	}
	return c.authenticatedRequest(request, verb, path, nil)
}

func (c *Client) MDMListCommands(opts automdm.MDMCommandListOptions) ([]*automdm.MDMCommand, error) {
	const defaultCommandsPerPage = 20

	verb, path := http.MethodGet, "/api/latest/automdm/commands"

	query := url.Values{}
	query.Set("per_page", fmt.Sprint(defaultCommandsPerPage))
	query.Set("order_key", "updated_at")
	query.Set("order_direction", "desc")
	query.Set("host_identifier", opts.Filters.HostIdentifier)
	query.Set("request_type", opts.Filters.RequestType)

	var statuses []string
	if len(opts.Filters.CommandStatuses) > 0 {
		statuses = make([]string, 0, len(opts.Filters.CommandStatuses))
		for _, s := range opts.Filters.CommandStatuses {
			statuses = append(statuses, string(s))
		}
	}
	query.Set("command_status", strings.Join(statuses, ","))

	var responseBody listMDMCommandsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query.Encode())
	if err != nil {
		return nil, err
	}

	return responseBody.Results, nil
}

func (c *Client) MDMGetCommandResults(commandUUID, hostIdentifier string) ([]*automdm.MDMCommandResult, error) {
	verb, path := http.MethodGet, "/api/latest/automdm/commands/results"

	query := url.Values{}
	query.Set("command_uuid", commandUUID)
	query.Set("host_identifier", hostIdentifier)

	var responseBody getMDMCommandResultsResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, query.Encode())
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	return responseBody.Results, nil
}

func (c *Client) RunMDMCommand(hostUUIDs []string, rawCmd []byte, forPlatform string) (*automdm.CommandEnqueueResult, error) {
	var prepareFn func([]byte) ([]byte, error)
	switch forPlatform {
	case "darwin":
		prepareFn = c.prepareAppleMDMCommand
	case "windows":
		prepareFn = c.prepareWindowsMDMCommand
	case "android":
		// Android commands are JSON; base64-encode as-is (no XML preparation needed).
		prepareFn = func(b []byte) ([]byte, error) { return b, nil }
	default:
		return nil, fmt.Errorf("Invalid platform %q. You can only run MDM commands on Windows, Apple, or Android hosts.", forPlatform)
	}

	rawCmd, err := prepareFn(rawCmd)
	if err != nil {
		return nil, err
	}

	request := runMDMCommandRequest{
		Command:   base64.RawStdEncoding.EncodeToString(rawCmd),
		HostUUIDs: hostUUIDs,
	}
	var response runMDMCommandResponse
	if err := c.authenticatedRequest(request, "POST", "/api/latest/automdm/commands/run", &response); err != nil {
		return nil, fmt.Errorf("run command request: %w", err)
	}
	return response.CommandEnqueueResult, nil
}

func (c *Client) prepareWindowsMDMCommand(rawCmd []byte) ([]byte, error) {
	if _, err := automdm.ParseWindowsMDMCommand(rawCmd); err != nil {
		return nil, err
	}

	// ensure there's a CmdID with a random UUID value, we're manipulating
	// the document this way to make sure we don't introduce any unintended
	// changes to the command XML.
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(rawCmd); err != nil {
		return nil, err
	}
	element := doc.FindElement("//CmdID")
	// if we can't find a CmdID, just add one.
	if element == nil {
		root := doc.Root()
		element = root.CreateElement("CmdID")
	}
	element.SetText(uuid.NewString())

	return doc.WriteToBytes()
}

func (c *Client) prepareAppleMDMCommand(rawCmd []byte) ([]byte, error) {
	var commandPayload map[string]interface{}
	if _, err := plist.Unmarshal(rawCmd, &commandPayload); err != nil {
		return nil, fmt.Errorf("The payload isn't valid XML. Please provide a file with valid XML: %w", err)
	}
	if commandPayload == nil {
		return nil, errors.New("The payload isn't valid. Please provide a valid MDM command in the form of a plist-encoded XML file.")
	}

	// generate a random command UUID
	commandPayload["CommandUUID"] = uuid.New().String()

	b, err := plist.Marshal(commandPayload, plist.XMLFormat)
	if err != nil {
		return nil, fmt.Errorf("marshal command plist: %w", err)
	}
	return b, nil
}

func (c *Client) MDMLockHost(hostID uint) error {
	var response automdm.LockHostResponse
	if err := c.authenticatedRequest(nil, "POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/lock", hostID), &response); err != nil {
		return fmt.Errorf("lock host request: %w", err)
	}
	return nil
}

func (c *Client) MDMUnlockHost(hostID uint) (*automdm.UnlockHostResponse, error) {
	var response automdm.UnlockHostResponse
	if err := c.authenticatedRequest(nil, "POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/unlock", hostID), &response); err != nil {
		return nil, fmt.Errorf("unlock host request: %w", err)
	}
	return &response, nil
}

func (c *Client) MDMWipeHost(hostID uint) error {
	var response automdm.WipeHostResponse
	if err := c.authenticatedRequest(nil, "POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/wipe", hostID), &response); err != nil {
		return fmt.Errorf("wipe host request: %w", err)
	}
	return nil
}

func (c *Client) MDMResetHost(hostID uint) error {
	var response automdm.ResetHostResponse
	if err := c.authenticatedRequest(nil, "POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/reset", hostID), &response); err != nil {
		return fmt.Errorf("reset host request: %w", err)
	}
	return nil
}

// MDMClearPasscodeHost issues a clear-passcode MDM command for the given host. Supported on
// Apple mobile (iOS/iPadOS) and Android hosts; the server returns 4xx for other platforms.
func (c *Client) MDMClearPasscodeHost(hostID uint) (*automdm.CommandEnqueueResult, error) {
	var response automdm.ClearPasscodeResponse
	if err := c.authenticatedRequest(nil, "POST", fmt.Sprintf("/api/latest/automdm/hosts/%d/clear_passcode", hostID), &response); err != nil {
		return nil, fmt.Errorf("clear passcode request: %w", err)
	}
	return response.CommandEnqueueResult, nil
}

type eulaContent struct {
	Bytes []byte
}

// eulaContent implements the bodyHandler interface so that we can read the
// response body directly into a byte slice which represents the EULA file content.
// This handler will be called in the Client.parseResponse method.
func (ec *eulaContent) Handle(res *http.Response) error {
	b, err := io.ReadAll(res.Body)
	ec.Bytes = b
	return err
}

const eulaAPIPath = "/api/latest/automdm/setup_experience/eula"

// eulaTarget is what differs between the macOS and Windows EULA requests; the
// client logic for both is the same. Requests by token don't need the
// platform: the token belongs to one EULA.
type eulaTarget struct {
	platform automdm.MDMEULAPlatform // sent for Windows only, so macOS requests stay as they always were
	label    string                  // names the EULA in error messages
}

var (
	macOSEULATarget   = eulaTarget{label: "eula"}
	windowsEULATarget = eulaTarget{platform: automdm.MDMEULAPlatformWindows, label: "Windows EULA"}
)

// metadataQuery is the platform parameter for the metadata request; uploads
// send it as a form field instead.
func (e eulaTarget) metadataQuery() string {
	if e.platform == "" {
		return ""
	}
	return "platform=" + string(e.platform)
}

func (c *Client) GetEULAContent(token string) ([]byte, error) {
	return c.getEULAContent(token)
}

func (c *Client) GetEULAMetadata() (*automdm.MDMEULA, error) {
	return c.getEULAMetadata(macOSEULATarget)
}

func (c *Client) DeleteEULAIfNeeded(dryRun bool) error {
	return c.deleteEULAIfNeeded(macOSEULATarget, dryRun)
}

func (c *Client) DeleteEULA(token string, dryRun bool) error {
	return c.deleteEULA(token, dryRun)
}

func (c *Client) UploadEULAIfNeeded(eulaPath string, dryRun bool) error {
	return c.uploadEULAIfNeeded(macOSEULATarget, eulaPath, dryRun)
}

func (c *Client) UploadEULA(eulaPath string, dryRun bool) error {
	return c.uploadEULA(macOSEULATarget, eulaPath, dryRun)
}

func (c *Client) GetWindowsEULAMetadata() (*automdm.MDMEULA, error) {
	return c.getEULAMetadata(windowsEULATarget)
}

func (c *Client) DeleteWindowsEULAIfNeeded(dryRun bool) error {
	return c.deleteEULAIfNeeded(windowsEULATarget, dryRun)
}

func (c *Client) UploadWindowsEULAIfNeeded(eulaPath string, dryRun bool) error {
	return c.uploadEULAIfNeeded(windowsEULATarget, eulaPath, dryRun)
}

func (c *Client) getEULAContent(token string) ([]byte, error) {
	verb, path := "GET", eulaAPIPath+"/"+token
	var responseBody eulaContent
	err := c.authenticatedRequest(nil, verb, path, &responseBody)
	return responseBody.Bytes, err
}

func (c *Client) getEULAMetadata(e eulaTarget) (*automdm.MDMEULA, error) {
	verb, path := "GET", eulaAPIPath+"/metadata"
	var responseBody getMDMEULAMetadataResponse
	if err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, e.metadataQuery()); err != nil {
		return nil, err
	}
	// A server older than automdmctl ignores the platform and answers for macOS;
	// deleting or replacing that EULA by mistake is worse than failing.
	if e.platform != "" && responseBody.Platform != e.platform {
		return nil, fmt.Errorf("the server didn't return the %s; it may be older than automdmctl", e.label)
	}
	return responseBody.MDMEULA, nil
}

func (c *Client) deleteEULAIfNeeded(e eulaTarget, dryRun bool) error {
	eula, err := c.getEULAMetadata(e)
	switch {
	case isNotFoundErr(err):
		// not found is OK, it means there is nothing to delete
		return nil
	case err != nil:
		return fmt.Errorf("getting %s metadata: %w", e.label, err)
	}

	err = c.deleteEULA(eula.Token, dryRun)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", e.label, err)
	}
	return nil
}

func (c *Client) deleteEULA(token string, dryRun bool) error {
	verb, path := "DELETE", eulaAPIPath+"/"+token
	var responseBody deleteMDMEULAResponse
	err := c.authenticatedRequestWithQuery(nil, verb, path, &responseBody, fmt.Sprintf("dry_run=%t", dryRun))
	return err
}

func (c *Client) uploadEULAIfNeeded(e eulaTarget, eulaPath string, dryRun bool) error {
	oldMeta, err := c.getEULAMetadata(e)
	// not found is OK, it means this is our first time uploading a eula
	if err != nil && !isNotFoundErr(err) {
		return fmt.Errorf("getting %s metadata: %w", e.label, err)
	}

	// read file to get the new file bytes
	eulaBytes, err := os.ReadFile(eulaPath)
	if err != nil {
		return fmt.Errorf("reading %s file: %w", e.label, err)
	}

	if oldMeta != nil {
		newChecksum := sha256.Sum256(eulaBytes)

		// compare checksums, if they're equal then we can skip the eula upload;
		// the server stores the sanitized name
		if bytes.Equal(oldMeta.Sha256, newChecksum[:]) && oldMeta.Name == automdm.SanitizeEULAFileName(filepath.Base(eulaPath)) {
			return nil
		}

		// The old one is deleted before the upload, as in the UI, so have the
		// server validate the new file first: a rejected file must not leave
		// devices without an agreement. If the upload still fails after the
		// delete, the next run finds none and uploads it.
		if !dryRun {
			if err := c.uploadEULA(e, eulaPath, true); err != nil {
				return err
			}
		}
		err = c.deleteEULA(oldMeta.Token, dryRun)
		if err != nil {
			return fmt.Errorf("deleting old %s: %w", e.label, err)
		}
	}

	return c.uploadEULA(e, eulaPath, dryRun)
}

func (c *Client) uploadEULA(e eulaTarget, eulaPath string, dryRun bool) error {
	verb, path := "POST", eulaAPIPath

	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	if e.platform != "" {
		if err := w.WriteField("platform", string(e.platform)); err != nil {
			return err
		}
	}

	// add the eula field
	fw, err := w.CreateFormFile("eula", filepath.Base(eulaPath))
	if err != nil {
		return err
	}

	file, err := os.Open(eulaPath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(fw, file); err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return fmt.Errorf("closing writer: %w", err)
	}

	resp, err := c.doContextWithBodyAndHeaders(context.Background(), verb, path, fmt.Sprintf("dry_run=%t", dryRun),
		b.Bytes(),
		map[string]string{
			"Content-Type":  w.FormDataContentType(),
			"Accept":        "application/json",
			"Authorization": fmt.Sprintf("Bearer %s", c.token),
		})
	if err != nil {
		return fmt.Errorf("do multipart request: %w", err)
	}
	defer resp.Body.Close()

	var eulaResponse createMDMEULAResponse
	if err := c.ParseResponse(verb, path, resp, &eulaResponse); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	return nil
}
