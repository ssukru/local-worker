package update

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var strictSemver = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

func NormalizeVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("version is required")
	}
	if !strictSemver.MatchString(value) {
		return "", fmt.Errorf("invalid semantic version %q", strings.TrimPrefix(value, "v"))
	}
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if !semver.IsValid(value) {
		return "", fmt.Errorf("invalid semantic version %q", strings.TrimPrefix(value, "v"))
	}
	return semver.Canonical(value), nil
}

func DisplayVersion(value string) (string, error) {
	normalized, err := NormalizeVersion(value)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(normalized, "v"), nil
}

func CompareVersions(current, target string) (int, error) {
	currentVersion, err := NormalizeVersion(current)
	if err != nil {
		return 0, err
	}
	targetVersion, err := NormalizeVersion(target)
	if err != nil {
		return 0, err
	}
	return semver.Compare(currentVersion, targetVersion), nil
}
