package dotnetversion

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// DefaultDailyURL serves the newest daily build of each channel, as redirects
// to the build itself.
const DefaultDailyURL = "https://aka.ms/dotnet"

// DailyChannel returns the channel a daily build of spec comes from: A.B or
// A.B.Cxx, or for A and latest the newest channel the index lists.
func (m *Metadata) DailyChannel(ctx context.Context, spec Spec, channel string) (string, error) {
	switch spec.Kind {
	case Channel, Exact:
		return fmt.Sprintf("%d.%d", spec.Major, spec.Minor), nil
	case Band:
		return fmt.Sprintf("%d.%d.%dxx", spec.Major, spec.Minor, spec.Band), nil
	}
	if spec.Kind == Latest && channel != "" && !isSupportChannel(channel) {
		return channel, nil
	}
	index, err := m.Index(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range index {
		if spec.Kind == Major && c.Major() != spec.Major {
			continue
		}
		if spec.Kind == Latest && isSupportChannel(channel) && !strings.EqualFold(c.ReleaseType, channel) {
			continue
		}
		return c.ChannelVersion, nil
	}
	return "", fmt.Errorf("no .NET release channel matches %q", spec)
}

// Daily resolves the newest daily build of comp in channel. The build and its
// .sha512 are looked up separately, so a build published in between is
// retried rather than paired with the wrong checksum.
func Daily(ctx context.Context, client *http.Client, baseURL, channel string, comp Component, rid, ext string) (Artifact, error) {
	if baseURL == "" {
		baseURL = DefaultDailyURL
	}
	link := fmt.Sprintf("%s/%s/daily/%s-%s%s", strings.TrimSuffix(baseURL, "/"), channel, comp.FilePrefix(), rid, ext)
	var lastErr error
	for range 2 {
		a, err := daily(ctx, client, link, comp)
		if err == nil {
			return a, nil
		}
		lastErr = err
	}
	return Artifact{}, lastErr
}

func daily(ctx context.Context, client *http.Client, link string, comp Component) (Artifact, error) {
	archiveURL, _, err := follow(ctx, client, link, false)
	if err != nil {
		return Artifact{}, err
	}
	sumURL, sum, err := follow(ctx, client, link+".sha512", true)
	if err != nil {
		return Artifact{}, err
	}
	build := path.Base(path.Dir(archiveURL))
	if path.Base(path.Dir(sumURL)) != build {
		return Artifact{}, fmt.Errorf("daily build changed while resolving %s", link)
	}
	v, err := Parse(build)
	if err != nil {
		return Artifact{}, fmt.Errorf("daily build %s: %w", archiveURL, err)
	}
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return Artifact{}, fmt.Errorf("empty checksum at %s", sumURL)
	}
	return Artifact{Component: comp, Version: v, URL: archiveURL, SHA512: strings.ToLower(fields[0])}, nil
}

// follow resolves link's redirects and returns the final URL, plus the body
// when read is set.
func follow(ctx context.Context, client *http.Client, link string, read bool) (string, string, error) {
	method := http.MethodHead
	if read {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, link, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", link, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("resolve %s: %s", link, resp.Status)
	}
	var body []byte
	if read {
		if body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<16)); err != nil {
			return "", "", err
		}
	}
	return resp.Request.URL.String(), string(body), nil
}
