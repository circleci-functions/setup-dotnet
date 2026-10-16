# setup-dotnet

Installs .NET SDKs and runtimes for the rest of the job, caches NuGet packages,
and restores and tests .NET projects, writing test results CircleCI can show
and split by.

```yaml
version: 2.1

functions:
  setup-dotnet: github.com/circleci-functions/setup-dotnet@v0.1.0

jobs:
  test:
    docker:
      - image: cimg/base:current
    parallelism: 2
    steps:
      - checkout
      - setup-dotnet:
          with:
            dotnet-version: "10.0.x"
      - setup-dotnet/restore
      - setup-dotnet/test
      - store_test_results:
          path: test-results
```

## Commands

| Command | Does |
|---|---|
| `setup-dotnet` | Installs SDKs and runtimes, puts `dotnet` on `PATH`, authenticates a NuGet source, and restores and saves the NuGet package cache |
| `setup-dotnet/restore` | `dotnet restore`, in locked mode when the project has lock files, then `dotnet tool restore` |
| `setup-dotnet/test` | `dotnet test`, writing JUnit XML for `store_test_results` and splitting test projects across parallel nodes |

## Installing .NET

```yaml
- setup-dotnet:
    with:
      dotnet-version: |
        8.0
        10.0.x
      runtime-version: "9.0"
```

| Flag | Default | Description |
|---|---|---|
| `dotnet-version` | | SDK versions, comma or newline separated. See the forms below |
| `dotnet-quality` | | `ga`, `preview` or `daily`. Ignored for an exact version |
| `dotnet-channel` | | With `dotnet-version: latest`: `LTS`, `STS`, `A.B` or `A.B.Cxx` |
| `global-json-file` | | `global.json` to install the SDK for. Empty finds one in the working directory or a parent, as `dotnet` does |
| `runtime-version` | | Runtimes to install alongside, in the same forms except `A.B.Cxx`. Each installs the ASP.NET Core Runtime, which includes the .NET Runtime, and on Windows the Windows Desktop Runtime too |
| `architecture` | the machine's | `x64`, `x86`, `arm64` or `arm` |
| `check-latest` | `true` | `false` reuses an SDK or runtime already installed that matches, without network. Also read from `$DOTNET_CHECK_LATEST` |
| `workloads` | | Workloads to install, e.g. `wasm-tools`, after `dotnet workload update` |
| `install-dir` | see below | Where to install .NET. Also read from `$DOTNET_INSTALL_DIR` |

| `dotnet-version` | Installs |
|---|---|
| `10.0.402`, `10.0.100-rc.2.25502.107` | exactly that SDK |
| `10.0`, `10.0.x` | the newest SDK of 10.0, a preview if 10.0 has no release yet |
| `10`, `10.x` | the newest SDK of the newest 10 channel |
| `10.0.1xx` | the newest SDK in that feature band |
| `latest` | the newest released SDK, narrowed by `dotnet-channel` |

A `global.json` in the working directory or a parent is always installed for,
alongside any `dotnet-version`, with its `rollForward` policy applied the way
the SDK applies it. With no `dotnet-version` and no `global.json`, setup-dotnet
uses the `dotnet` already on `PATH`, or installs the newest LTS SDK when there
is none.

Versions resolve against Microsoft's release metadata, and every download is
checked against the SHA-512 it publishes. When the newest release of a floating
version is listed but its download is not up yet, the previous one is
installed instead.

### Where .NET goes

Everything setup-dotnet installs shares one .NET installation, so SDKs and
runtimes sit side by side and `dotnet` sees all of them.

- **Linux and macOS:** `~/.dotnet`, which needs no root. `dotnet` is linked into
  a directory already on `PATH`, so every later step runs it, whatever its shell.
- **Windows:** next to the .NET already in `C:\Program Files\dotnet` when that is
  writable, so cmd, PowerShell and bash steps all run it. Otherwise
  `%LOCALAPPDATA%\Microsoft\dotnet`.

A later bash step also gets `DOTNET_ROOT`, for apps and global tools started by
name, and the global tools directory on `PATH`.

When the installation holds a newer SDK than the one asked for, which happens
when installing next to an existing .NET, `dotnet` uses the newest unless a
`global.json` says otherwise. setup-dotnet warns when that is the case.

## NuGet

### Caching

On by default. setup-dotnet restores the NuGet global packages folder when it
runs and saves it at the end of the job, including a job that fails, is
cancelled or times out. Any later `dotnet restore`, `build` or `test` fills
in what is missing.

The cache key is a hash of every `packages.lock.json` under the working
directory, or with no lock file, of every project file, `Directory.Packages.props`,
`Directory.Build.props`, `nuget.config` and `global.json`. When the key changes,
the restore starts from the newest cache saved in the same three-day window
rather than from empty; every three days the cache starts over, so it cannot
grow without bound.

| Flag | Default | Description |
|---|---|---|
| `cache` | `true` | `false` turns off every cache |
| `nuget-cache` | `true` | Cache the NuGet global packages folder |
| `sdk-cache` | `true` | Keep downloaded SDK and runtime archives in the job cache |
| `cache-dependency-path` | | Globs, relative to the working directory, of the files to key the NuGet cache on, in place of the defaults |

### Private feeds

```yaml
- setup-dotnet:
    with:
      dotnet-version: "10.0.x"
      source-url: https://nuget.pkg.github.com/my-org/index.json
      token-env: GITHUB_PACKAGES_TOKEN
```

| Flag | Default | Description |
|---|---|---|
| `source-url` | | NuGet source to authenticate. Also becomes the default push source |
| `token-env` | `NUGET_AUTH_TOKEN` | Environment variable holding the token, for example from a context |
| `owner` | `$CIRCLE_PROJECT_USERNAME` | User name for the source |
| `config-file` | `nuget.config` in the working directory | Its sources whose URL matches `source-url` get the credentials. With none, the source is added |

The credentials go into a file of their own in the user's NuGet configuration
directory, which NuGet merges with the rest; neither the project's nor the
user's `NuGet.Config` is changed.

For several feeds with different tokens, define them in your `nuget.config` and
set NuGet's own `NuGetPackageSourceCredentials_<source name>` variable, e.g.
`Username=me;Password=$TOKEN`, for each one in a context.

## Restoring

```yaml
- setup-dotnet/restore:
    with:
      project: src/MyApp.sln
```

| Flag | Default | Description |
|---|---|---|
| `project` | | Solution, project or directory. Empty lets `dotnet` find one in the working directory |
| `locked-mode` | `true` | Pass `--locked-mode` when a `packages.lock.json` exists, so a restore that would change a lock file fails |
| `tool-restore` | `true` | Run `dotnet tool restore` when a tool manifest exists |
| `extra-args` | | More arguments for `dotnet restore`, split as a shell would but never evaluated |

## Testing

```yaml
- setup-dotnet/test:
    with:
      project: MyApp.sln
      filter: Category!=Integration
      hang-timeout: 9m
- store_test_results:
    path: test-results
```

| Flag | Default | Description |
|---|---|---|
| `project` | | Solution, project or directory. Empty lets `dotnet` find one in the working directory |
| `filter` | | Filter expression for `dotnet test --filter` |
| `results-dir` | `test-results` | Where to write JUnit XML |
| `split` | `true` | With `parallelism`, run only this node's share of the test projects |
| `hang-timeout` | | Fail a test that runs longer than this, e.g. `9m`, with a hang dump, rather than the step timing out with no output |
| `extra-args` | | More arguments for `dotnet test`, e.g. `-c Release --no-build` |

Test results are converted from the TRX files `dotnet test` writes, so test
projects need no extra logger package. xUnit, NUnit and MSTest all work.

With `parallelism`, the test projects are split across nodes by timing, using
the timings of earlier runs. Each test case's `file` is its project, which is
what the split goes by.

When `global.json` selects Microsoft.Testing.Platform as the test runner,
setup-dotnet passes that platform's options instead: `--report-trx`, and for
`hang-timeout`, `--hangdump`, which needs the
`Microsoft.Testing.Extensions.HangDump` package in the test project.

## Platforms

Linux on x64, arm64 and arm, including musl distributions such as Alpine;
macOS on arm64 and x64; Windows on x64, arm64 and x86. Docker and machine
executors alike, with or without .NET already installed.

Alpine needs the libraries .NET runs on: `icu-libs krb5-libs libgcc libintl libssl3 libstdc++ zlib`.
