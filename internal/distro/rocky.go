package distro

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"github.com/PuerkitoBio/goquery"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

const (
	rockyPubPath   = "/pub/rocky"
	rockyVaultPath = "/vault/rocky"

	rockyFlavorDVD = "dvd"
	rockyFlavorNet = "boot"

	rockyISODirectory = "isos"
)

var (
	rockyVersionLink      = regexp.MustCompile(`^(\d+(?:\.\d+)?)/$`)
	rockyISOLinkRegexTmpl = template.Must(template.New("isolink").Parse(`^Rocky-(\d+(?:\.\d+)?(?:-\d+(?:\.\d+)?)?)-{{ .ArchRegexSafe }}-{{ .FlavorRegexSafe }}.iso$`))
	rockyISODirectoryTmpl = template.Must(template.New("isodirectory").Parse("isos/{{ .Arch }}"))

	errNoVersionsSatisfyingConstraint = errors.New("could not find any versions satisfying constraint")
	errNoISOsForArchFlavorCombination = errors.New("could not find any ISOs for the given arch and flavor")
	errCorruptedMetadata              = errors.New("distro metadata is corrupted")
	errMirrorHasNoISOs                = errors.New("could not find ISOs directory in mirror for given distribution/version constraints")
)

type rockyProvider struct {
	logger *slog.Logger
	client *http.Client

	mirrorURL  *url.URL
	flavor     string
	constraint *semver.Constraints
}

type rockyOptions struct {
	MirrorURL  string `mapstructure:"mirror_url" default:"https://dl.rockylinux.org"`
	NetInstall bool   `mapstructure:"net_install" default:"false"`
}

// Vault isn't available from mirrors, hence use the upstream Rocky site for this
var rockyVaultBase = url.URL{
	Scheme: "https",
	Host:   "dl.rockylinux.org",
}

func newRocky(logger *slog.Logger, versionConstraint string, client *http.Client, opts *rockyOptions) (*rockyProvider, error) {
	if client == nil {
		client = http.DefaultClient
	}

	mirrorURL, err := url.Parse(opts.MirrorURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mirror URL '%s': %w", opts.MirrorURL, err)
	}

	constraint, err := semver.NewConstraint(versionConstraint)
	if err != nil {
		return nil, fmt.Errorf("invalid version constraint: %w", err)
	}

	flavor := rockyFlavorDVD
	if opts.NetInstall {
		flavor = rockyFlavorNet
	}

	return &rockyProvider{
		logger:     logger,
		constraint: constraint,
		client:     client,
		mirrorURL:  mirrorURL,
		flavor:     flavor,
	}, nil
}

func (r *rockyProvider) Latest(arches []string) (map[string]downloader, error) {
	_, downloadDirectory, err := r.latestVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to check latest Rocky version: %w", err)
	}

	downloaders := make(map[string]downloader, len(arches))

	for _, arch := range arches {
		_, isoURL, err := r.latestISO(downloadDirectory, arch)
		if err != nil {
			return nil, fmt.Errorf("failed to find latest ISO for arch '%s': %w", arch, err)
		}

		// TODO fix this checksum
		checksum, err := r.checksum(*isoURL)
		if err != nil {
			return nil, fmt.Errorf("could not get ISO checksum for arch '%s': %w", arch, err)
		}

		h := sha256.New()
		if _, err := h.Write(checksum); err != nil {
			panic(fmt.Sprintf("failed to compute hash of checksum: %v", err))
		}

		hash := fmt.Sprintf("%x", h.Sum(nil))

		downloaders[arch] = &isoDownloader{
			logger: r.logger,
			client: r.client,
			url:    isoURL,
			hash:   hash,
			metadataMaker: func(directory string) (*metadata, error) {
				return &metadata{
					// TODO: probably no need to have the hash in metadata
					Hash:       hash,
					InitrdPath: "isolinux/initrd.img",
					KernelPath: "isolinux/vmlinuz",
				}, nil
			},
		}
	}

	return downloaders, nil
}

func (r *rockyProvider) latestVersion() (*semver.Version, *url.URL, error) {
	pubVersions, err := r.listDirectory(r.mirrorURL.JoinPath(rockyPubPath), rockyVersionLink)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list published Rocky versions: %w", err)
	}

	vaultVersions, err := r.listDirectory(rockyVaultBase.JoinPath(rockyVaultPath), rockyVersionLink)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list archived Rocky versions: %w", err)
	}

	// Prefer published versions, because these use the (fast!) mirror that the user provides,
	// and not the (hella slow) Rocky upstream download site
	pubVersions = append(pubVersions, vaultVersions...)

	var latestVersion *semver.Version
	var latestEntries []*directoryEntry

	for _, entry := range pubVersions {
		version, err := semver.NewVersion(entry.submatch)
		if err != nil {
			r.logger.Warn("failed to parse Rocky version",
				"version", entry.submatch,
				"error", err,
			)
			continue
		}

		// Skip: doesn't fit constraint
		if !r.constraint.Check(version) {
			continue
		}

		// Skip: older version
		if latestVersion != nil && version.LessThan(latestVersion) {
			continue
		}

		if version.Equal(latestVersion) {
			latestEntries = append(latestEntries, entry)
		} else {
			latestEntries = []*directoryEntry{entry}
			latestVersion = version
		}
	}

	if latestEntries == nil {
		return nil, nil, errNoVersionsSatisfyingConstraint
	}

	// Non-current Rocky versions will not have an ISO directory. There's no good way to
	// check this other than checking whether the ISO directory exists: for non-current
	// versions, under 'pub/', there will be no ISO directory; under 'vault/', there will
	// be.
	// Arguably we could just have checked the latest version that we saw in the listing,
	// but I don't trust the Rocky maintainers enough to be sure that the latest version
	// we see in there will be valid and the current version.
	for _, entry := range latestEntries {
		hasISOs, err := r.hasISODirectory(entry.href)
		if err != nil {
			return nil, nil, fmt.Errorf("error while crawling Rocky mirror for ISO directory: %w", err)
		}

		// Greedily return here, as the earlier entry in the latestEntries slice will be
		// from the mirror, and not from the upstream Rocky download site.
		if hasISOs {
			return latestVersion, entry.href, nil
		}
	}

	return nil, nil, errMirrorHasNoISOs
}

func (r *rockyProvider) hasISODirectory(base *url.URL) (bool, error) {
	resp, err := r.client.Get(base.JoinPath(rockyISODirectory).String())
	if err != nil {
		return false, fmt.Errorf("failed to get ISO directory: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, newHTTPError(resp)
	}
}

func (r *rockyProvider) latestISO(directoryURL *url.URL, arch string) (*semver.Version, *url.URL, error) {
	tmplArgs := struct {
		Arch            string
		ArchRegexSafe   string
		Flavor          string
		FlavorRegexSafe string
	}{
		Arch:            arch,
		ArchRegexSafe:   regexp.QuoteMeta(arch),
		Flavor:          r.flavor,
		FlavorRegexSafe: regexp.QuoteMeta(r.flavor),
	}

	isoDirectory := &bytes.Buffer{}
	if err := rockyISODirectoryTmpl.Execute(isoDirectory, tmplArgs); err != nil {
		// This is not a user error, and should never happen, since isoDirectory is static
		panic(fmt.Sprintf("error executing Rocky ISO directory template: %v", err))
	}

	isoRegexBuff := &bytes.Buffer{}
	if err := rockyISOLinkRegexTmpl.Execute(isoRegexBuff, tmplArgs); err != nil {
		// This is not a user error, and should never happen, since isoRegexBuff is static
		panic(fmt.Sprintf("error executing Rocky ISO filename regex template: %v", err))
	}

	isoRegex, err := regexp.Compile(isoRegexBuff.String())
	if err != nil {
		panic(fmt.Sprintf("error compiling Rocky ISO filename regex: %v", err))
	}

	isos, err := r.listDirectory(directoryURL.JoinPath(isoDirectory.String()), isoRegex)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list available ISOs: %w", err)
	}

	var latestISO *directoryEntry
	var latestVersion *semver.Version

	for _, iso := range isos {
		versionString, dateString, isDateRelease := strings.Cut(iso.submatch, "-")

		version, err := semver.NewVersion(versionString)
		if err != nil {
			r.logger.Warn("ISO detected in Rocky download mirror with unparsable version",
				"version", versionString,
				"filename", iso.title,
				"href", iso.href.String(),
			)
			continue
		}

		// Version can be suffixed with -DATE. Semver interprets this as a prerelease version, but
		// it's actually a *post*release version.
		// The default release for '9.1' by the semver library is '9.1.0', so if we set the
		// release field to the date release, then it should be seen as 'newer' (which is what
		// we want!)
		if isDateRelease {
			// The date strings have a ".NUMBER" suffix on them -- presumably to differentiate in
			// the case that they need to release two ISOs on the same date.
			dateString, dateRelease, hasDateRelease := strings.Cut(dateString, ".")
			if hasDateRelease {
				dateString += fmt.Sprintf("%03s", dateRelease)
			} else {
				dateString += "000"
			}

			date, err := strconv.ParseUint(dateString, 10, 64)
			if err != nil {
				r.logger.Warn("ISO matched with invalid date string. This is a bug, and should not happen.",
					"date", dateString,
					"version", versionString,
					"filename", iso.title,
					"href", iso.href.String(),
				)
				continue
			}

			version = semver.New(version.Major(), version.Minor(), date, "", "")
		}

		// This version is older; continue looking
		if latestVersion != nil && version.LessThan(latestVersion) {
			continue
		}

		latestVersion = version
		latestISO = iso
	}

	if latestISO == nil {
		return nil, nil, errNoISOsForArchFlavorCombination
	}

	return latestVersion, latestISO.href, nil
}

func (r *rockyProvider) checksum(isoURL url.URL) ([]byte, error) {
	isoURL.Path += ".CHECKSUM"
	resp, err := r.client.Get(isoURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to download checksum: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, newHTTPError(resp)
	}

	checksum, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read checksum: %w", err)
	}

	return checksum, nil
}

type directoryEntry struct {
	title    string
	submatch string
	href     *url.URL
}

func (r *rockyProvider) listDirectory(directory *url.URL, regex *regexp.Regexp) ([]*directoryEntry, error) {
	resp, err := r.client.Get(directory.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get directory listing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, newHTTPError(resp)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse directory listing HTML: %w", err)
	}

	versions := []*directoryEntry{}

	doc.Find("body a").Each(func(_ int, s *goquery.Selection) {
		matches := regex.FindStringSubmatch(s.Text())
		if matches == nil {
			return
		}

		href, hrefExists := s.Attr("href")
		if !hrefExists {
			return
		}

		submatch := ""
		if len(matches) > 1 {
			submatch = matches[1]
		}

		versions = append(versions, &directoryEntry{
			title:    matches[0],
			submatch: submatch,
			href:     directory.JoinPath(href),
		})
	})

	return versions, nil
}

type rockyMetadata struct {
	Test string
}

type httpError struct {
	url    string
	status int
	body   []byte
}

func newHTTPError(resp *http.Response) *httpError {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		body = []byte(fmt.Sprintf("(failed to read body: %v)", err))
	}

	return &httpError{status: resp.StatusCode, body: body, url: resp.Request.URL.String()}
}

func (h *httpError) Error() string {
	return fmt.Sprintf("http request to '%s' failed with status %d and body '%s'", h.url, h.status, string(h.body))
}

type retryableError struct {
	wrapped error
}

func (e *retryableError) Error() string {
	return e.wrapped.Error()
}

func (e *retryableError) Unwrap() error {
	return e.wrapped
}
