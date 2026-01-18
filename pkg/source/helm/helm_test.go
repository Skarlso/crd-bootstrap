package helm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendFilesToCrds(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(t *testing.T, root string)
		expectedContains []string
		expectedCount    int
	}{
		{
			name: "single file in root",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.WriteFile(filepath.Join(root, "crd1.yaml"), []byte("kind: CRD1"), 0o644))
			},
			expectedContains: []string{"kind: CRD1"},
			expectedCount:    1,
		},
		{
			name: "multiple files in root",
			setup: func(t *testing.T, root string) {
				require.NoError(t, os.WriteFile(filepath.Join(root, "crd1.yaml"), []byte("kind: CRD1"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(root, "crd2.yaml"), []byte("kind: CRD2"), 0o644))
			},
			expectedContains: []string{"kind: CRD1", "kind: CRD2"},
			expectedCount:    2,
		},
		{
			name: "files in subdirectories",
			setup: func(t *testing.T, root string) {
				subdir := filepath.Join(root, "subdir")
				require.NoError(t, os.MkdirAll(subdir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "crd1.yaml"), []byte("kind: CRD1"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(subdir, "crd2.yaml"), []byte("kind: CRD2"), 0o644))
			},
			expectedContains: []string{"kind: CRD1", "kind: CRD2"},
			expectedCount:    2,
		},
		{
			name: "deeply nested subdirectories",
			setup: func(t *testing.T, root string) {
				deep := filepath.Join(root, "a", "b", "c")
				require.NoError(t, os.MkdirAll(deep, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "root.yaml"), []byte("content-root"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(root, "a", "a.yaml"), []byte("content-a"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(deep, "deep.yaml"), []byte("content-deep"), 0o644))
			},
			expectedContains: []string{"content-root", "content-a", "content-deep"},
			expectedCount:    3,
		},
		{
			name:             "empty directory",
			setup:            func(t *testing.T, root string) {},
			expectedContains: nil,
			expectedCount:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)

			outputFile := filepath.Join(t.TempDir(), "crds.yaml")
			crds, err := os.Create(outputFile)
			require.NoError(t, err)

			s := &Source{}
			err = s.appendFilesToCrds(root, crds)
			require.NoError(t, err)
			require.NoError(t, crds.Close())

			content, err := os.ReadFile(outputFile)
			require.NoError(t, err)

			for _, expected := range tt.expectedContains {
				assert.Contains(t, string(content), expected)
			}

			if tt.expectedCount > 0 {
				assert.Equal(t, tt.expectedCount, strings.Count(string(content), "---\n"))
			}
		})
	}
}

func TestAppendFilesToCrdsErrorOnInvalidRoot(t *testing.T) {
	outputFile := filepath.Join(t.TempDir(), "crds.yaml")
	crds, err := os.Create(outputFile)
	require.NoError(t, err)
	defer crds.Close()

	s := &Source{}
	err = s.appendFilesToCrds("/nonexistent/path", crds)
	assert.Error(t, err)
}
