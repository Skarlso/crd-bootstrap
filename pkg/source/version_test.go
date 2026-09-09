package source

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

func bootstrap(constraint, lastApplied string) *v1alpha1.Bootstrap {
	obj := &v1alpha1.Bootstrap{}
	obj.Spec.Version.Semver = constraint
	obj.Status.LastAppliedRevision = lastApplied

	return obj
}

func TestCheckSemverUpdate(t *testing.T) {
	tests := []struct {
		name             string
		latest           string
		constraint       string
		lastApplied      string
		expectedUpdate   bool
		expectedRevision string
		expectedErr      string
	}{
		{
			name:             "first apply",
			latest:           "v1.2.0",
			constraint:       ">=v1",
			expectedUpdate:   true,
			expectedRevision: "v1.2.0",
		},
		{
			name:             "newer version rolls forward",
			latest:           "v1.3.0",
			constraint:       ">=v1",
			lastApplied:      "v1.2.0",
			expectedUpdate:   true,
			expectedRevision: "v1.3.0",
		},
		{
			name:             "same version is not reapplied",
			latest:           "v1.2.0",
			constraint:       ">=v1",
			lastApplied:      "v1.2.0",
			expectedRevision: "v1.2.0",
		},
		{
			name:             "older version does not roll back",
			latest:           "v1.1.0",
			constraint:       ">=v1",
			lastApplied:      "v1.2.0",
			expectedRevision: "v1.2.0",
		},
		{
			name:             "latest outside the constraint is ignored",
			latest:           "v2.0.0",
			constraint:       "~1.2",
			lastApplied:      "v1.2.0",
			expectedRevision: "v1.2.0",
		},
		{
			name:        "unparsable latest version",
			latest:      "not-a-version",
			constraint:  ">=v1",
			expectedErr: "failed to parse current version 'not-a-version'",
		},
		{
			name:        "unparsable constraint",
			latest:      "v1.2.0",
			constraint:  "!!!",
			expectedErr: "failed to parse constraint",
		},
		{
			name:        "digest as last applied revision means the source type was switched",
			latest:      "v1.2.0",
			constraint:  ">=v1",
			lastApplied: "7162957068d512154ed353d31b9a0a5a9ff148b4611bd85ba704467a4fcd101a",
			expectedErr: "expected a version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update, revision, err := CheckSemverUpdate(tt.latest, bootstrap(tt.constraint, tt.lastApplied))

			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedUpdate, update)
			assert.Equal(t, tt.expectedRevision, revision)
		})
	}
}

func TestLatestMatchingVersion(t *testing.T) {
	tests := []struct {
		name       string
		versions   []string
		constraint string
		expected   string
		expectErr  bool
	}{
		{
			name:       "picks the highest match regardless of input order",
			versions:   []string{"v1.1.0", "v1.3.0", "v1.2.0"},
			constraint: ">=v1",
			expected:   "v1.3.0",
		},
		{
			name:       "ignores versions outside the constraint",
			versions:   []string{"v1.2.0", "v2.0.0"},
			constraint: "~1.2",
			expected:   "v1.2.0",
		},
		{
			name:       "skips values that are not semver",
			versions:   []string{"latest", "v1.2.0", "main"},
			constraint: ">=v1",
			expected:   "v1.2.0",
		},
		{
			name:       "no version satisfies the constraint",
			versions:   []string{"v1.0.0", "v1.1.0"},
			constraint: ">=v2",
			expectErr:  true,
		},
		{
			name:       "no versions at all",
			versions:   nil,
			constraint: ">=v1",
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constraint, err := semver.NewConstraint(tt.constraint)
			require.NoError(t, err)

			latest, err := LatestMatchingVersion(tt.versions, constraint)
			if tt.expectErr {
				require.ErrorIs(t, err, ErrNoMatchingVersion)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, latest)
		})
	}
}
