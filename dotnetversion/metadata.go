package dotnetversion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// DefaultMetadataURL is where Microsoft publishes releases-index.json and the
// per-channel releases.json files.
const DefaultMetadataURL = "https://builds.dotnet.microsoft.com/dotnet/release-metadata"

// ChannelInfo is one entry of releases-index.json.
type ChannelInfo struct {
	ChannelVersion string `json:"channel-version"`
	LatestSDK      string `json:"latest-sdk"`
	LatestRuntime  string `json:"latest-runtime"`
	SupportPhase   string `json:"support-phase"`
	ReleaseType    string `json:"release-type"`
	ReleasesJSON   string `json:"releases.json"`
}

// Major returns the channel's major version, or -1 if it cannot be parsed.
func (c ChannelInfo) Major() int {
	major, _ := c.MajorMinor()
	return major
}

// MajorMinor returns the channel's version, or -1, -1 if it cannot be parsed.
func (c ChannelInfo) MajorMinor() (int, int) {
	a, b, ok := strings.Cut(c.ChannelVersion, ".")
	if !ok {
		return -1, -1
	}
	major, err1 := strconv.Atoi(a)
	minor, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return -1, -1
	}
	return major, minor
}

// Prerelease reports whether the channel has no GA release yet.
func (c ChannelInfo) Prerelease() bool {
	return c.SupportPhase == "preview" || c.SupportPhase == "go-live"
}

type release struct {
	ReleaseVersion string    `json:"release-version"`
	Runtime        *product  `json:"runtime"`
	SDK            *product  `json:"sdk"`
	SDKs           []product `json:"sdks"`
	AspNetCore     *product  `json:"aspnetcore-runtime"`
	WindowsDesktop *product  `json:"windowsdesktop"`
}

type product struct {
	Version string `json:"version"`
	Files   []file `json:"files"`
}

type file struct {
	Name string `json:"name"`
	RID  string `json:"rid"`
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

// Metadata fetches release metadata, once per document per process.
type Metadata struct {
	BaseURL string
	HTTP    *http.Client

	mu       sync.Mutex
	index    []ChannelInfo
	releases map[string][]release
}

// NewMetadata returns a Metadata reading from baseURL, or from
// DefaultMetadataURL when baseURL is empty.
func NewMetadata(baseURL string, client *http.Client) *Metadata {
	if baseURL == "" {
		baseURL = DefaultMetadataURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Metadata{BaseURL: strings.TrimSuffix(baseURL, "/"), HTTP: client, releases: map[string][]release{}}
}

// Index returns the channels in releases-index.json, newest first.
func (m *Metadata) Index(ctx context.Context) ([]ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index != nil {
		return m.index, nil
	}
	var doc struct {
		Index []ChannelInfo `json:"releases-index"`
	}
	if err := m.get(ctx, m.BaseURL+"/releases-index.json", &doc); err != nil {
		return nil, err
	}
	sortChannels(doc.Index)
	m.index = doc.Index
	return m.index, nil
}

func (m *Metadata) channelReleases(ctx context.Context, ch ChannelInfo) ([]release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.releases[ch.ChannelVersion]; ok {
		return r, nil
	}
	var doc struct {
		Releases []release `json:"releases"`
	}
	if err := m.get(ctx, m.BaseURL+"/"+ch.ChannelVersion+"/releases.json", &doc); err != nil {
		return nil, err
	}
	m.releases[ch.ChannelVersion] = doc.Releases
	return doc.Releases, nil
}

func (m *Metadata) get(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("parse %s: %w", url, err)
	}
	return nil
}

func sortChannels(chs []ChannelInfo) {
	slices.SortStableFunc(chs, func(a, b ChannelInfo) int {
		am, an := a.MajorMinor()
		bm, bn := b.MajorMinor()
		if am != bm {
			return bm - am
		}
		return bn - an
	})
}
