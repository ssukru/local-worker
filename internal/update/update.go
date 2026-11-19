package update

import (
	"context"
	"fmt"
	"runtime"
)

const (
	MaxArchiveBytes  = 128 << 20
	MaxChecksumBytes = 2 << 20
	MaxBinaryBytes   = 128 << 20
)

type Plan struct {
	CurrentVersion string
	TargetVersion  string
	Release        Release
	Executable     Executable
	Comparison     int
	Development    bool
}

type Updater struct {
	Client            *Client
	GOOS              string
	GOARCH            string
	ResolveExecutable func(string) (Executable, error)
	ValidateVersion   VersionValidator
}

func NewUpdater() *Updater {
	return &Updater{
		Client:            NewClient(),
		GOOS:              runtime.GOOS,
		GOARCH:            runtime.GOARCH,
		ResolveExecutable: ResolveExecutable,
		ValidateVersion:   BinaryVersion,
	}
}

func (u *Updater) Plan(ctx context.Context, currentVersion, requestedVersion, argv0 string) (Plan, error) {
	resolver := u.ResolveExecutable
	if resolver == nil {
		resolver = ResolveExecutable
	}
	executable, err := resolver(argv0)
	if err != nil {
		return Plan{}, err
	}
	client := u.Client
	if client == nil {
		client = NewClient()
	}
	release, err := client.Resolve(ctx, requestedVersion, u.GOOS, u.GOARCH)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		CurrentVersion: currentVersion,
		TargetVersion:  release.Version,
		Release:        release,
		Executable:     executable,
		Development:    currentVersion == "dev",
	}
	if !plan.Development {
		comparison, err := CompareVersions(currentVersion, release.Version)
		if err != nil {
			return Plan{}, fmt.Errorf("current lw version %q is not valid semantic version: %w", currentVersion, err)
		}
		plan.Comparison = comparison
	}
	return plan, nil
}

func (u *Updater) Apply(ctx context.Context, plan Plan) error {
	if plan.Executable.Manager == ManagerHomebrew {
		return fmt.Errorf("lw is managed by Homebrew")
	}
	client := u.Client
	if client == nil {
		client = NewClient()
	}
	checksums, err := client.Download(ctx, plan.Release.Checksums, MaxChecksumBytes)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected, err := ExpectedChecksum(checksums, plan.Release.Archive.Name)
	if err != nil {
		return err
	}
	archive, err := client.Download(ctx, plan.Release.Archive, MaxArchiveBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", plan.Release.Archive.Name, err)
	}
	if err := VerifySHA256(archive, expected); err != nil {
		return fmt.Errorf("verify %s: %w", plan.Release.Archive.Name, err)
	}
	binary, err := ExtractBinary(archive, MaxBinaryBytes)
	if err != nil {
		return fmt.Errorf("extract %s: %w", plan.Release.Archive.Name, err)
	}
	return Replace(ctx, plan.Executable, binary, plan.TargetVersion, u.ValidateVersion)
}
