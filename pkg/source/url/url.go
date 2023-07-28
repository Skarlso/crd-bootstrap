package url

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Source provides functionality to fetch a CRD yaml from a GitHub release.
type Source struct {
	Client *http.Client

	client client.Client
	next   source.Contract
}

var _ source.Contract = &Source{}

// NewSource creates a new GitHub handling Source.
func NewSource(client client.Client, next source.Contract) *Source {
	return &Source{client: client, next: next}
}

func (s *Source) FetchCRD(ctx context.Context, dir string, obj *v1alpha1.Bootstrap, revision string) (string, error) {
	if obj.Spec.Source.URL == nil {
		if s.next == nil {
			return "", fmt.Errorf("url isn't defined and there are no other sources configured")
		}

		return s.next.FetchCRD(ctx, dir, obj, revision)
	}

	if err := s.fetch(ctx, dir, obj); err != nil {
		return "", fmt.Errorf("failed to fetch CRD: %w", err)
	}

	return filepath.Join(dir, "crds.yaml"), nil
}

func (s *Source) HasUpdate(ctx context.Context, obj *v1alpha1.Bootstrap) (bool, string, error) {
	if obj.Spec.Source.URL == nil {
		if s.next == nil {
			return false, "", fmt.Errorf("github isn't defined and there are no other sources configured")
		}

		return s.next.HasUpdate(ctx, obj)
	}

	dir, err := os.MkdirTemp("", "crd-url")
	if err != nil {
		return false, "", fmt.Errorf("failed to create temp folder: %w", err)
	}

	defer os.RemoveAll(dir)

	if err := s.fetch(ctx, dir, obj); err != nil {
		return false, "", fmt.Errorf("failed to fetch CRD: %w", err)
	}

	file, err := os.Open(filepath.Join(dir, "crds.yaml"))
	if err != nil {
		return false, "", fmt.Errorf("failed to open file of downloaded CRD: %w", err)
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, "", fmt.Errorf("failed to hash content of CRD: %w", err)
	}

	sum := hash.Sum(nil)
	if obj.Spec.Version.Digest != "" {
		// we will always apply it, it should be safe because there shouldn't be any changes.
		if obj.Spec.Version.Digest == hex.EncodeToString(sum) {
			return true, obj.Spec.Version.Digest, nil
		}

		return false, "", nil
	}

	if obj.Status.LastAppliedRevision == hex.EncodeToString(sum) {
		return false, obj.Status.LastAppliedRevision, nil
	}

	return true, hex.EncodeToString(sum), nil
}

// fetch fetches the content.
func (s *Source) fetch(ctx context.Context, dir string, obj *v1alpha1.Bootstrap) error {
	downloadURL := obj.Spec.Source.URL.URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request for %s, error: %w", downloadURL, err)
	}

	// download
	c := http.DefaultClient
	if obj.Spec.Source.URL.SecretRef != nil {
		c, err = s.constructAuthenticatedClient(ctx, obj)
		if err != nil {
			return fmt.Errorf("failed to construct authenticated client: %w", err)
		}
	}

	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to download content from %s, error: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	// check response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download content from %s, status: %s", downloadURL, resp.Status)
	}

	wf, err := os.Create(filepath.Join(dir, "crds.yaml"))
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	if _, err := io.Copy(wf, resp.Body); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	return nil
}

func (s *Source) constructAuthenticatedClient(ctx context.Context, obj *v1alpha1.Bootstrap) (*http.Client, error) {
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: obj.Spec.Source.URL.SecretRef.Name, Namespace: obj.Namespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to find secret ref for token: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("token key not found in provided secret")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: string(token)},
	)

	return oauth2.NewClient(ctx, ts), nil
}
