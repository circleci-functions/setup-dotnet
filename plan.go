package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/circleci-functions/setup-dotnet/dotnetdist"
	"github.com/circleci-functions/setup-dotnet/dotnetversion"
	"github.com/circleci-functions/setup-dotnet/globaljson"
)

// want is one thing to install: a spec, or the SDK a global.json asks for.
type want struct {
	component  dotnetversion.Component
	spec       dotnetversion.Spec
	channel    string
	constraint *globaljson.Constraint
	label      string
}

func (w want) floating() bool {
	return w.constraint != nil || w.spec.Floating()
}

func (w want) describe() string {
	if w.constraint != nil {
		return w.label
	}
	return w.spec.String()
}

// installed reports whether root already has a version that meets w. A
// latest with a channel is never met offline, since only the index knows
// which releases a channel holds.
func (w want) installed(root dotnetdist.Root, quality, channel string) bool {
	have := root.Versions(w.component)
	if w.constraint != nil {
		return len(w.constraint.Select(have)) > 0
	}
	if w.channel != "" {
		channel = w.channel
	}
	if w.spec.Kind == dotnetversion.Latest && channel != "" {
		return false
	}
	return slices.ContainsFunc(have, func(v dotnetversion.Version) bool {
		if !w.spec.Matches(v) {
			return false
		}
		pre := v.IsPrerelease() && w.spec.Kind != dotnetversion.Exact
		return !pre || (quality != "ga" && (w.spec.Kind != dotnetversion.Latest || quality == "preview"))
	})
}

// expectedSDK is the SDK dotnet should pick in the working directory: what
// global.json selects, else the newest SDK asked for.
func expectedSDK(root dotnetdist.Root, wants []want) (dotnetversion.Version, bool) {
	have := root.Versions(dotnetversion.SDK)
	for _, w := range wants {
		if w.constraint != nil {
			if picked := w.constraint.Select(have); len(picked) > 0 {
				return picked[0], true
			}
			return dotnetversion.Version{}, false
		}
	}
	var best dotnetversion.Version
	found := false
	for _, w := range wants {
		if w.component != dotnetversion.SDK {
			continue
		}
		for _, v := range have {
			if w.spec.Matches(v) && (!found || v.Compare(best) > 0) {
				best, found = v, true
			}
		}
	}
	return best, found
}

type resolver struct {
	metadata *dotnetversion.Metadata
	dailyURL string
	quality  string
	channel  string
	rid      string
	ext      string
}

// resolve returns the archives that meet w, best first.
func (r *resolver) resolve(ctx context.Context, w want) ([]dotnetversion.Artifact, error) {
	if w.constraint != nil {
		return r.resolveGlobalJSON(ctx, w)
	}
	channel := r.channel
	if w.channel != "" {
		channel = w.channel
	}
	quality := r.quality
	if quality != "" && !qualityApplies(w.spec) {
		logf("--dotnet-quality is ignored for %s", w.spec)
		quality = ""
	}
	if quality == "daily" {
		ch, err := r.metadata.DailyChannel(ctx, w.spec, channel)
		if err != nil {
			return nil, err
		}
		a, err := dotnetversion.Daily(ctx, http.DefaultClient, r.dailyURL, ch, w.component, r.rid, r.ext)
		if err != nil {
			return nil, err
		}
		return []dotnetversion.Artifact{a}, nil
	}
	return r.metadata.Resolve(ctx, dotnetversion.Query{
		Spec: w.spec, Component: w.component, Quality: quality, Channel: channel, RID: r.rid, Ext: r.ext,
	})
}

// qualityApplies mirrors where a build quality means something: a floating
// spec on .NET 6 or later.
func qualityApplies(s dotnetversion.Spec) bool {
	switch s.Kind {
	case dotnetversion.Exact:
		return false
	case dotnetversion.Latest:
		return true
	}
	return s.Major > 5
}

// resolveGlobalJSON reads channels nearest first, or newest first for the
// latest* policies, and returns the first channel's SDKs that global.json
// accepts, best first.
func (r *resolver) resolveGlobalJSON(ctx context.Context, w want) ([]dotnetversion.Artifact, error) {
	index, err := r.metadata.Index(ctx)
	if err != nil {
		return nil, err
	}
	channels := slices.Clone(index)
	if w.constraint.Version != nil && !strings.HasPrefix(string(w.constraint.Policy), "latest") {
		slices.Reverse(channels)
	}
	for _, ch := range channels {
		major, minor := ch.MajorMinor()
		if !w.constraint.Channels(major, minor) {
			continue
		}
		available, err := r.metadata.Available(ctx, ch, dotnetversion.SDK, r.rid, r.ext)
		if err != nil {
			return nil, err
		}
		versions := make([]dotnetversion.Version, 0, len(available))
		for _, a := range available {
			versions = append(versions, a.Version)
		}
		picked := w.constraint.Select(versions)
		if len(picked) == 0 {
			continue
		}
		out := make([]dotnetversion.Artifact, 0, len(picked))
		for _, v := range picked {
			i := slices.IndexFunc(available, func(a dotnetversion.Artifact) bool { return a.Version.Compare(v) == 0 })
			out = append(out, available[i])
		}
		return out, nil
	}
	return nil, fmt.Errorf("no published .NET SDK for %s satisfies %s", r.rid, w.label)
}
