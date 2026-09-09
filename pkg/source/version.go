package source

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

// ErrNoMatchingVersion is returned when none of the versions offered by a source satisfy the constraint.
var ErrNoMatchingVersion = errors.New("no version satisfies the constraint")

// CheckSemverUpdate decides whether latestVersion should be applied. It rolls forward only: a version
// equal to or below the last applied revision is never reapplied, so an upstream that drops a release
// cannot roll the cluster back.
func CheckSemverUpdate(latestVersion string, obj *v1alpha1.Bootstrap) (bool, string, error) {
	latest, err := semver.NewVersion(latestVersion)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse current version '%s' as semver: %w", latestVersion, err)
	}

	constraint, err := semver.NewConstraint(obj.Spec.Version.Semver)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse constraint: %w", err)
	}

	if !constraint.Check(latest) {
		return false, obj.Status.LastAppliedRevision, nil
	}

	if obj.Status.LastAppliedRevision == "" {
		return true, latestVersion, nil
	}

	// A digest here means the source type was switched on an existing object, which we don't support.
	lastApplied, err := semver.NewVersion(obj.Status.LastAppliedRevision)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse last applied revision '%s'; expected a version: %w", obj.Status.LastAppliedRevision, err)
	}

	if !lastApplied.LessThan(latest) {
		return false, obj.Status.LastAppliedRevision, nil
	}

	return true, latestVersion, nil
}

// LatestMatchingVersion returns the highest version out of versions that satisfies the constraint.
// Values that don't parse as semver are skipped. Returns ErrNoMatchingVersion if nothing matches.
func LatestMatchingVersion(versions []string, constraint *semver.Constraints) (string, error) {
	var latest *semver.Version

	for _, v := range versions {
		parsed, err := semver.NewVersion(v)
		if err != nil {
			continue
		}

		if !constraint.Check(parsed) {
			continue
		}

		if latest == nil || parsed.GreaterThan(latest) {
			latest = parsed
		}
	}

	if latest == nil {
		return "", fmt.Errorf("%w: %s", ErrNoMatchingVersion, constraint.String())
	}

	return latest.Original(), nil
}
