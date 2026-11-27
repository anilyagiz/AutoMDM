package automdmctl

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/packaging"
	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
	"github.com/ghodss/yaml"
	zlog "github.com/rs/zerolog/log"
)

// teamNameLookupTimeout bounds the whole lookup: automdmctl's API client opts out of the default HTTP timeout, and a
// wrong --automdm-url must not stall the package build.
var teamNameLookupTimeout = 10 * time.Second

// packageAutoMDM is what the package file name needs about the team an enroll secret belongs to: its name, and its ID
// with the other teams, to tell apart teams whose names clean up the same.
type packageAutoMDM struct {
	Name   string
	ID     uint
	Others []packaging.AutoMDM
}

// automdmForPackage finds the team that enrollSecret belongs to, asking the server through the automdmctl config contexts
// logged in to automdmURL. It returns the zero value whenever the team can't be found, which never fails the build: AutoMDM
// Free, a global (Unassigned) secret, a role that can't read enroll secrets (AutoMDM masks them), no matching context, or
// any error. Only an invalid or expired token prints a note, to errWriter.
func automdmForPackage(configPath, automdmURL, enrollSecret string, errWriter io.Writer) packageAutoMDM {
	target, ok := normalizeServerURL(automdmURL)
	if !ok || enrollSecret == "" {
		return packageAutoMDM{}
	}
	// Read the config directly: `package` must not create ~/.automdm/config as a side effect.
	b, err := os.ReadFile(configPath)
	if err != nil {
		zlog.Debug().Err(err).Msg("team name lookup: read automdmctl config")
		return packageAutoMDM{}
	}
	var config configFile
	if err := yaml.Unmarshal(b, &config); err != nil {
		zlog.Debug().Err(err).Msg("team name lookup: parse automdmctl config")
		return packageAutoMDM{}
	}

	var names []string
	for name, cc := range config.Contexts {
		if address, ok := normalizeServerURL(joinURLPrefix(cc.Address, cc.URLPrefix)); ok && address == target && cc.Token != "" {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == "default":
			return -1
		case b == "default":
			return 1
		}
		return strings.Compare(a, b)
	})

	deadline := time.Now().Add(teamNameLookupTimeout)
	listed, unauthenticated := false, false
	for _, name := range names {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			break
		}
		found, err := automdmFromServer(config.Contexts[name], enrollSecret, timeout)
		switch {
		case err == nil && found.Name != "":
			return found
		case err == nil:
			listed = true
			zlog.Debug().Str("context", name).Msg("team name lookup: no team visible to this user has the enroll secret")
		default:
			unauthenticated = unauthenticated || errors.Is(err, service.ErrUnauthenticated)
			zlog.Debug().Err(err).Str("context", name).Msg("team name lookup: list teams")
		}
	}
	if unauthenticated && !listed {
		fmt.Fprintf(errWriter, "Note: the team's name was left out of the package's filename because the API token for %s is invalid or "+
			"expired. To add it, log in with: automdmctl login\n", automdmURL)
	}
	return packageAutoMDM{}
}

// automdmFromServer lists the teams visible to the context's user and returns the one with enrollSecret, or the zero
// value if none has it.
func automdmFromServer(cc Context, enrollSecret string, timeout time.Duration) (packageAutoMDM, error) {
	options := []service.ClientOption{service.SetClientErrorWriter(io.Discard)}
	if len(cc.CustomHeaders) > 0 {
		options = append(options, service.WithCustomHeaders(cc.CustomHeaders))
	}
	client, err := service.NewClient(cc.Address, cc.TLSSkipVerify, cc.RootCA, cc.URLPrefix, options...)
	if err != nil {
		return packageAutoMDM{}, err
	}
	httpClient, ok := client.HTTP.(*http.Client)
	if !ok {
		return packageAutoMDM{}, errors.New("unexpected HTTP client type")
	}
	httpClient.Timeout = timeout
	client.SetToken(cc.Token)

	teams, err := client.ListTeams("")
	if err != nil {
		return packageAutoMDM{}, err
	}
	for _, f := range teams {
		for _, s := range f.Secrets {
			// AutoMDM masks the secrets the user can't read, so a masked one never identifies the team.
			if s != nil && s.Secret != automdm.MaskedPassword && s.Secret == enrollSecret {
				match := packageAutoMDM{Name: f.Name, ID: f.ID}
				for _, other := range teams {
					if other.ID != f.ID {
						match.Others = append(match.Others, packaging.AutoMDM{ID: other.ID, Name: other.Name})
					}
				}
				return match, nil
			}
		}
	}
	return packageAutoMDM{}, nil
}

func joinURLPrefix(address, prefix string) string {
	if prefix = strings.Trim(prefix, "/"); prefix == "" {
		return address
	}
	return strings.TrimRight(address, "/") + "/" + prefix
}

// normalizeServerURL returns rawURL as scheme://host[:port][/path], lowercased, without a default port or a trailing
// slash, so that equivalent spellings of a server URL compare equal. A URL without a scheme is taken as https, since
// --automdm-url's help says "host:port".
func normalizeServerURL(rawURL string) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", false
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" || strings.Contains(host, ":") {
		host = net.JoinHostPort(host, port)
		host = strings.TrimSuffix(host, ":")
	}
	return scheme + "://" + host + strings.TrimRight(u.Path, "/"), true
}
