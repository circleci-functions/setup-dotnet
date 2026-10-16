// Command setup-dotnet installs .NET SDKs and runtimes in a CircleCI job,
// caches NuGet packages, and restores and tests .NET projects.
//
// Supported platforms: linux (glibc and musl), darwin, windows.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"
)

type cli struct {
	Install installCmd `cmd:"" default:"withargs" help:"Install .NET SDKs and runtimes, authenticate a NuGet source and cache NuGet packages. Runs when no command is given."`
	Restore restoreCmd `cmd:"" help:"Restore NuGet packages and local tools."`
	Test    testCmd    `cmd:"" help:"Run dotnet test, writing JUnit results for store_test_results and splitting test projects across parallel nodes."`
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-dotnet"),
		kong.Description("Set up .NET in a CircleCI job: install SDKs and runtimes, cache NuGet packages, restore and test."),
		kong.UsageOnError(),
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
