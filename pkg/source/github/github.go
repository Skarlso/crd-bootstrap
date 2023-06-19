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
)

// Source provides functionality to fetch a CRD yaml from a GitHub release.
// TODO: add secrets to fetch github token.
type Source struct {
	BaseURL       string
	ReleaseAPIURL string
	Client        *http.Client
}

// GetLatestVersion calls the GitHub API and returns the latest released version.
func GetLatestVersion() (string, error) {
	c := http.DefaultClient
	c.Timeout = 15 * time.Second

	res, err := c.Get("o.ReleaseAPIURL" + "/latest")
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
func (o *Source) ExistingVersion(version string) (bool, error) {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	ghURL := fmt.Sprintf(o.ReleaseAPIURL+"/tags/%s", version)
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

func (o *Source) fetch(ctx context.Context, version, dir string) error {
	ghURL := fmt.Sprintf("%s/latest/download/install.yaml", o.BaseURL)
	if strings.HasPrefix(version, "v") {
		ghURL = fmt.Sprintf("%s/download/%s/install.yaml", o.BaseURL, version)
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
