package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Source provides functionality to fetch a CRD yaml from a GitHub release.
// TODO: add secrets to fetch github token.
type Source struct {
	BaseURL       string
	ReleaseAPIURL string
	Client        *http.Client

	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new GitHub handling Source.
func NewSource(client client.Client, next source.Contract) *Source {
	return &Source{client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, source v1alpha1.Source, revision string) (string, error) {
	if source.GitHub == nil {
		if s.next == nil {
			return "", fmt.Errorf("github isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, source, revision)
	}

	if err := s.fetch(ctx, revision, dir); err != nil {
		return "", fmt.Errorf("failed to fetch CRD: %w", err)
	}

	return "", nil
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	//TODO implement me
	panic("implement me")
}

// GetLatestVersion calls the GitHub API and returns the latest released version.
func (s *Source) GetLatestVersion() (string, error) {
	c := http.DefaultClient
	c.Timeout = 15 * time.Second

	res, err := c.Get(s.ReleaseAPIURL + "/latest")
	if err != nil {
		return "", fmt.Errorf("GitHub API call failed: %w", err)
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	type meta struct {
		Tag string `json:"tag_name"`
	}
	var m meta
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("decoding GitHub API response failed: %w", err)
	}

	return m.Tag, err
}

// ExistingVersion calls the GitHub API to confirm the given version does exist.
func (s *Source) ExistingVersion(version string) (bool, error) {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	ghURL := fmt.Sprintf(s.ReleaseAPIURL+"/tags/%s", version)
	c := http.DefaultClient
	c.Timeout = 15 * time.Second

	res, err := c.Get(ghURL)
	if err != nil {
		return false, fmt.Errorf("GitHub API call failed: %w", err)
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GitHub API returned an unexpected status code (%d)", res.StatusCode)
	}
}

func (s *Source) fetch(ctx context.Context, version, dir string) error {
	ghURL := fmt.Sprintf("%s/latest/download/install.yaml", s.BaseURL)
	if strings.HasPrefix(version, "v") {
		ghURL = fmt.Sprintf("%s/download/%s/install.yaml", s.BaseURL, version)
	}

	req, err := http.NewRequest("GET", ghURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request for %s, error: %w", ghURL, err)
	}

	// download
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to download manifests.tar.gz from %s, error: %w", ghURL, err)
	}
	defer resp.Body.Close()

	// check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download manifests.tar.gz from %s, status: %s", ghURL, resp.Status)
	}

	wf, err := os.OpenFile(filepath.Join(dir, "install.yaml"), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	if _, err := io.Copy(wf, resp.Body); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	return nil
}
