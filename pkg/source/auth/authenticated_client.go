package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstructAuthenticatedClient creates an authenticated http Client.
func ConstructAuthenticatedClient(ctx context.Context, client client.Client, name, namespace string) (*http.Client, error) {
	secret := &corev1.Secret{}
	if err := client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to find secret ref for token: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return nil, errors.New("token key not found in provided secret")
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: string(token)},
	)

	return oauth2.NewClient(ctx, ts), nil
}
