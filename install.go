package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/pathlink"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-dotnet/dotnetdist"
	"github.com/circleci-functions/setup-dotnet/dotnetpath"
	"github.com/circleci-functions/setup-dotnet/dotnetversion"
	"github.com/circleci-functions/setup-dotnet/globaljson"
	"github.com/circleci-functions/setup-dotnet/nuget"
)

type installCmd struct {
	DotnetVersion  string `help:"SDK versions to install, comma or newline separated: A.B.C, A.B, A.B.x, A, A.x, A.B.Cxx or latest. Empty installs what global.json asks for, else keeps the dotnet on PATH, else installs the latest LTS." example:"10.0.x"`
	DotnetQuality  string `enum:",ga,preview,daily" default:"" help:"Build quality: ga, preview or daily. Ignored for an exact version."`
	DotnetChannel  string `help:"Channel for --dotnet-version=latest: LTS, STS, A.B or A.B.Cxx."`
	GlobalJSONFile string `name:"global-json-file" help:"global.json to install the SDK for. Empty finds one in the working directory or a parent, as dotnet does."`
	RuntimeVersion string `help:"Runtime versions to install alongside, comma or newline separated, in the same forms except A.B.Cxx. Each installs the ASP.NET Core Runtime, which includes the .NET Runtime, and on Windows the Windows Desktop Runtime too."`
	Architecture   string `help:"Architecture to install: x64, x86, arm64 or arm. Empty is the machine's."`
	CheckLatest    bool   `default:"true" negatable:"" env:"DOTNET_CHECK_LATEST" help:"Resolve versions online and install the newest match. false reuses a matching SDK or runtime already installed, without network."`
	Workloads      string `help:"Workloads to install, comma or newline separated, e.g. wasm-tools."`
	InstallDir     string `env:"DOTNET_INSTALL_DIR" help:"Directory to install .NET into. Empty is ~/.dotnet, or on Windows the .NET already on PATH when it is writable, else %LOCALAPPDATA%\\Microsoft\\dotnet."`

	Cache               bool   `default:"true" negatable:"" help:"Use the job cache. false turns off every cache below."`
	NugetCache          bool   `name:"nuget-cache" default:"true" negatable:"" help:"Restore the NuGet global packages folder now, and save it at the end of the job."`
	SDKCache            bool   `name:"sdk-cache" default:"true" negatable:"" help:"Keep downloaded SDK and runtime archives in the job cache."`
	CacheDependencyPath string `help:"Files whose contents key the NuGet cache, as globs relative to the working directory, comma or newline separated. Empty uses every packages.lock.json, else every project file."`

	SourceURL  string `name:"source-url" help:"NuGet source to authenticate, with the token in the --token-env variable. Also becomes the default push source."`
	Owner      string `help:"User name for the source. Empty is $CIRCLE_PROJECT_USERNAME, which suits GitHub Packages."`
	ConfigFile string `help:"Existing NuGet.config whose sources matching --source-url get the credentials. Empty is the nuget.config in the working directory."`
	TokenEnv   string `default:"NUGET_AUTH_TOKEN" help:"Name of the environment variable holding the token for --source-url."`

	MetadataURL string `hidden:"" help:"Release metadata base URL."`
	DailyURL    string `hidden:"" help:"Daily build base URL."`
}

func (c *installCmd) Run(ctx context.Context, client *sdk.Client) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	pl, err := c.plan(wd)
	if err != nil {
		return err
	}
	r := &resolver{
		metadata: dotnetversion.NewMetadata(c.MetadataURL, http.DefaultClient),
		dailyURL: c.DailyURL, quality: c.DotnetQuality, channel: c.DotnetChannel, rid: pl.rid, ext: pl.platform.Ext(),
	}
	root := pl.root
	if c.CheckLatest || pl.crossArch {
		err = c.install(ctx, client, r, dotnetdist.Root(root), pl.wants)
	} else {
		root, err = c.reuseOrInstall(ctx, client, r, root, pl.existing, pl.wants)
	}
	if err != nil {
		return err
	}
	return c.finish(ctx, client, pl, root, wd)
}

// plan is what Run decided before installing anything.
type plan struct {
	platform     dotnetdist.Platform
	crossArch    bool
	rid          string
	wants        []want
	sdkRequested bool
	existing     string
	root         string
}

func (c *installCmd) plan(wd string) (plan, error) {
	var pl plan
	var err error
	if pl.platform, pl.crossArch, err = c.platform(); err != nil {
		return pl, err
	}
	if pl.rid, err = pl.platform.RID(); err != nil {
		return pl, err
	}
	if pl.wants, pl.sdkRequested, err = c.wants(wd, pl.platform); err != nil {
		return pl, err
	}
	pl.existing = existingRoot()
	if pl.root, err = c.root(pl.platform, pl.crossArch, pl.existing, pl.sdkRequested); err != nil {
		return pl, err
	}
	if len(pl.wants) == 0 && pl.existing == "" {
		pl.wants, pl.sdkRequested = []want{latestLTS()}, true
	}
	return pl, nil
}

// finish puts root's dotnet on PATH and sets up what needs an SDK there.
func (c *installCmd) finish(ctx context.Context, client *sdk.Client, pl plan, root, wd string) error {
	if err := c.putOnPath(root, pl.existing, pl.platform, pl.crossArch); err != nil {
		return err
	}
	if pl.sdkRequested {
		checkSelectedSDK(root, wd, pl.wants)
	}
	if err := c.installWorkloads(root, wd); err != nil {
		return err
	}
	if err := c.authenticate(wd); err != nil {
		return err
	}
	if c.Cache && c.NugetCache {
		c.setUpNuGetCache(ctx, client, root, wd)
	}
	return nil
}

func (c *installCmd) platform() (dotnetdist.Platform, bool, error) {
	p := dotnetdist.Current()
	if c.Architecture == "" {
		return p, false, nil
	}
	arch, err := dotnetdist.ParseArch(c.Architecture)
	if err != nil {
		return p, false, err
	}
	cross := arch != p.Arch
	p.Arch = arch
	return p, cross, nil
}

// wants lists what to install: the SDKs asked for, the global.json one, and
// the runtimes. sdkRequested is false when only runtimes are.
func (c *installCmd) wants(wd string, p dotnetdist.Platform) ([]want, bool, error) {
	if c.DotnetChannel != "" && !strings.EqualFold(strings.TrimSpace(c.DotnetVersion), "latest") {
		logf("--dotnet-channel only applies to --dotnet-version=latest, so it is ignored")
	}
	sdks, err := dotnetversion.ParseSpecs(c.DotnetVersion)
	if err != nil {
		return nil, false, err
	}
	var wants []want
	for _, s := range sdks {
		wants = append(wants, want{component: dotnetversion.SDK, spec: s})
	}
	gj, err := c.globalJSON(wd)
	if err != nil {
		return nil, false, err
	}
	if gj != nil && gj.HasSDK() {
		constraint, err := gj.Constraint()
		if err != nil {
			return nil, false, err
		}
		wants = append(wants, want{component: dotnetversion.SDK, constraint: &constraint, label: gj.Path})
	}
	sdkRequested := len(wants) > 0

	runtimes, err := dotnetversion.ParseSpecs(c.RuntimeVersion)
	if err != nil {
		return nil, false, err
	}
	for _, s := range runtimes {
		wants = append(wants, want{component: dotnetversion.ASPNETCoreRuntime, spec: s})
		if p.OS == "windows" {
			wants = append(wants, want{component: dotnetversion.WindowsDesktopRuntime, spec: s})
		}
	}
	return wants, sdkRequested, nil
}

func (c *installCmd) globalJSON(wd string) (*globaljson.File, error) {
	path := c.GlobalJSONFile
	if path == "" {
		found, err := globaljson.Find(wd)
		if err != nil || found == "" {
			return nil, err
		}
		path = found
	}
	f, err := globaljson.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("global-json-file %s does not exist", path)
	}
	return f, err
}

func latestLTS() want {
	spec, _ := dotnetversion.ParseSpec("latest")
	return want{component: dotnetversion.SDK, spec: spec, channel: "LTS"}
}

// root picks the .NET root to install into.
func (c *installCmd) root(p dotnetdist.Platform, crossArch bool, existing string, sdkRequested bool) (string, error) {
	archDir := func(dir string) string {
		if crossArch {
			return filepath.Join(dir, p.Arch)
		}
		return dir
	}
	switch {
	case c.InstallDir != "":
		dir, err := filepath.Abs(expandHome(c.InstallDir))
		return archDir(dir), err
	case existing != "" && !crossArch && !sdkRequested:
		return existing, nil
	case existing != "" && !crossArch && p.OS == "windows" && pathlink.Writable(existing):
		return existing, nil
	}
	if p.OS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("LOCALAPPDATA is not set; set --install-dir")
		}
		return archDir(filepath.Join(local, "Microsoft", "dotnet")), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return archDir(filepath.Join(home, ".dotnet")), nil
}

// existingRoot returns the root of the dotnet already on PATH, or "".
func existingRoot() string {
	bin, err := lookDotnet()
	if err != nil {
		return ""
	}
	root, err := dotnetpath.Probe(bin)
	if err != nil {
		return ""
	}
	return root
}

func (c *installCmd) install(ctx context.Context, client *sdk.Client, r *resolver, root dotnetdist.Root, wants []want) error {
	if len(wants) == 0 {
		logf("using the .NET at %s", root.Dir())
		return nil
	}
	if !pathlink.Writable(root.Dir()) {
		return fmt.Errorf("cannot write to %s; set --install-dir to a writable directory", root.Dir())
	}
	inst := &dotnetdist.Installer{Root: root, CacheDir: archiveDir(), HTTP: http.DefaultClient, Log: os.Stderr}
	if c.Cache && c.SDKCache {
		inst.Cache = archiveCache{client: client}
	}
	for _, w := range wants {
		candidates, err := r.resolve(ctx, w)
		if err != nil {
			return err
		}
		if err := installFirst(ctx, inst, w, candidates); err != nil {
			return err
		}
	}
	return nil
}

// installFirst installs the first candidate that can be downloaded. Only a
// floating spec moves on, when the newest release's archive is not published
// yet.
func installFirst(ctx context.Context, inst *dotnetdist.Installer, w want, candidates []dotnetversion.Artifact) error {
	const maxTries = 3
	for i, a := range candidates {
		installed, err := inst.Install(ctx, a)
		if errors.Is(err, dotnetdist.ErrNotFound) && w.floating() && i+1 < len(candidates) && i+1 < maxTries {
			logf("%s %s is not downloadable yet, so installing %s %s", a.Component, a.Version, a.Component, candidates[i+1].Version)
			continue
		}
		if err != nil {
			return fmt.Errorf("%s %s: %w", a.Component, a.Version, err)
		}
		verb := "is already in"
		if installed {
			verb = "installed in"
		}
		logf("%s %s %s %s", a.Component, a.Version, verb, inst.Root.Dir())
		return nil
	}
	return fmt.Errorf("no %s matching %s could be installed", w.component, w.describe())
}

// reuseOrInstall serves check-latest=false: a root that already has a match
// for every want is used as it is, root before existing. Otherwise the wants
// root lacks are installed into it.
func (c *installCmd) reuseOrInstall(ctx context.Context, client *sdk.Client, r *resolver, root, existing string, wants []want) (string, error) {
	for _, dir := range []string{root, existing} {
		if dir != "" && satisfiesAll(dotnetdist.Root(dir), wants, c.DotnetQuality, c.DotnetChannel) {
			logf("using the .NET at %s, which has a match for every version asked for", dir)
			return dir, nil
		}
	}
	var missing []want
	for _, w := range wants {
		if !w.installed(dotnetdist.Root(root), c.DotnetQuality, c.DotnetChannel) {
			missing = append(missing, w)
		}
	}
	return root, c.install(ctx, client, r, dotnetdist.Root(root), missing)
}

func satisfiesAll(root dotnetdist.Root, wants []want, quality, channel string) bool {
	for _, w := range wants {
		if !w.installed(root, quality, channel) {
			return false
		}
	}
	return len(wants) > 0
}

// putOnPath makes root's dotnet the one later steps run, and exports
// DOTNET_ROOT and the global tools directory to bash steps.
func (c *installCmd) putOnPath(root, existing string, p dotnetdist.Platform, crossArch bool) error {
	var path []string
	if existing == "" || !sameDir(root, existing) {
		res, err := dotnetpath.Link(dotnetpath.Options{Root: root})
		if err != nil {
			return err
		}
		if res.Shadowed != "" {
			logf("the dotnet.exe in %s comes first on PATH and belongs to another .NET, so it is left alone: "+
				"bash steps get %s first on PATH, cmd and PowerShell steps still run the other one", res.Shadowed, root)
			path = append(path, root)
		} else {
			logf("dotnet resolves from %s", res.Dir)
		}
	}
	if tools, err := dotnetpath.ToolsDir(); err == nil {
		path = append(path, tools)
	}
	rootVar := "DOTNET_ROOT"
	if crossArch {
		rootVar += "_" + strings.ToUpper(p.Arch)
	}
	if err := dotnetpath.Export(map[string]string{rootVar: root}, path); err != nil {
		logf("%s and the tools directory are not exported to later steps: %v", rootVar, err)
	}
	return nil
}

// checkSelectedSDK warns when dotnet in wd would pick a different SDK from
// the one asked for, which happens when the root holds newer ones and no
// global.json pins it.
func checkSelectedSDK(root, wd string, wants []want) {
	expected, ok := expectedSDK(dotnetdist.Root(root), wants)
	if !ok {
		return
	}
	cmd := dotnet(root, "--version")
	cmd.Dir = wd
	cmd.Stdout, cmd.Stderr = nil, nil
	out, err := cmd.Output()
	if err != nil {
		logf("dotnet --version failed in %s: %v", wd, err)
		return
	}
	got := strings.TrimSpace(string(out))
	logf(".NET SDK %s is the one dotnet uses in %s", got, wd)
	if got != expected.String() {
		logf("warning: %s holds newer SDKs than %s; add a global.json to pin the SDK dotnet uses", root, expected)
	}
}

func (c *installCmd) installWorkloads(root, wd string) error {
	ids := splitList(c.Workloads)
	if len(ids) == 0 {
		return nil
	}
	for _, args := range [][]string{{"workload", "update"}, append([]string{"workload", "install"}, ids...)} {
		cmd := dotnet(root, args...)
		cmd.Dir = wd
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("dotnet %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

func (c *installCmd) authenticate(wd string) error {
	if c.SourceURL == "" {
		return nil
	}
	token := os.Getenv(c.TokenEnv)
	if token == "" {
		return fmt.Errorf("$%s is empty: set it to the token for %s, or name another variable with --token-env", c.TokenEnv, c.SourceURL)
	}
	owner := c.Owner
	if owner == "" {
		owner = os.Getenv("CIRCLE_PROJECT_USERNAME")
	}
	existing := c.ConfigFile
	if existing == "" {
		existing = nuget.FindConfig(wd)
	}
	dir, err := nuget.UserConfigDir()
	if err != nil {
		return err
	}
	p, err := nuget.Authenticate(dir, existing, nuget.Source{URL: c.SourceURL, Username: owner, Token: token})
	if err != nil {
		return err
	}
	logf("credentials for %s written to %s", c.SourceURL, p)
	return nil
}

// setUpNuGetCache restores the global packages folder and registers its save
// for the end of the job, so a failed or cancelled job keeps what it fetched.
func (c *installCmd) setUpNuGetCache(ctx context.Context, client *sdk.Client, root, wd string) {
	if len(dotnetdist.Root(root).Versions(dotnetversion.SDK)) == 0 {
		return
	}
	files, err := nuget.DependencyFiles(wd, splitList(c.CacheDependencyPath))
	if err != nil {
		logf("NuGet cache skipped: %v", err)
		return
	}
	if len(files) == 0 {
		logf("NuGet cache skipped: no packages.lock.json or project file under %s", wd)
		return
	}
	hash, err := nuget.Hash(wd, files)
	if err != nil {
		logf("NuGet cache skipped: %v", err)
		return
	}
	cmd := dotnet(root, "nuget", "locals", "global-packages", "--list")
	cmd.Dir, cmd.Stdout, cmd.Stderr = wd, nil, nil
	dir, err := nuget.GlobalPackagesDir(cmd)
	if err != nil {
		logf("NuGet cache skipped: %v", err)
		return
	}
	save, restore := nuget.Keys(sdk.OSArch(), hash, time.Now())
	_, err = client.RestoreCache(ctx, restore, sdk.WithStepName("Restoring NuGet package cache"))
	sdk.LogCacheErr("restore NuGet packages", err)
	err = client.AtTeardown().SaveCache(ctx, save, []string{dir}, sdk.WithStepName("Saving NuGet package cache"))
	sdk.LogCacheErr("save NuGet packages", err)
}

type archiveCache struct{ client *sdk.Client }

func (a archiveCache) Restore(ctx context.Context, key string) {
	_, err := a.client.RestoreCache(ctx, []string{key}, sdk.WithStepName("Restoring .NET archive cache"))
	sdk.LogCacheErr("restore .NET archive", err)
}

func (a archiveCache) Save(ctx context.Context, key string, paths []string) {
	sdk.LogCacheErr("save .NET archive", a.client.SaveCache(ctx, key, paths, sdk.WithStepName("Saving .NET archive cache")))
}

func archiveDir() string {
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "setup-dotnet", "archives")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "setup-dotnet", "archives")
}
