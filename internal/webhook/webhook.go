/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

// Server manages webhook endpoints for Bootstrap objects.
type Server struct {
	client     client.Client
	router     *mux.Router
	triggers   map[string]chan struct{}
	mu         sync.RWMutex
	port       int
	httpServer *http.Server
}

// WebhookPayload represents the expected webhook payload structure.
type WebhookPayload struct {
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Release struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
	} `json:"release"`
	Action string `json:"action"`
	Ref    string `json:"ref"`
}

// NewServer creates a new webhook server.
func NewServer(client client.Client, port int) *Server {
	s := &Server{
		client:   client,
		router:   mux.NewRouter(),
		triggers: make(map[string]chan struct{}),
		port:     port,
	}

	s.router.HandleFunc("/webhook/{name}", s.handleWebhook).Methods("POST")
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	return s
}

// Start starts the webhook server.
func (s *Server) Start(ctx context.Context) error {
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.router,
	}

	logger := log.FromContext(ctx)
	logger.Info("Starting webhook server", "port", s.port)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error(err, "Failed to shutdown webhook server")
		}
	}()

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook server failed: %w", err)
	}

	return nil
}

// RegisterBootstrap registers a Bootstrap object for webhook notifications.
func (s *Server) RegisterBootstrap(name, namespace string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	if ch, exists := s.triggers[key]; exists {
		return ch
	}

	ch := make(chan struct{}, 1)
	s.triggers[key] = ch
	return ch
}

// UnregisterBootstrap removes a Bootstrap object from webhook notifications.
func (s *Server) UnregisterBootstrap(name, namespace string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	if ch, exists := s.triggers[key]; exists {
		close(ch)
		delete(s.triggers, key)
	}
}

// handleWebhook processes incoming webhook requests.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	logger := log.FromContext(r.Context()).WithValues("webhook", name)
	logger.Info("Received webhook request")

	// Get the Bootstrap object to validate the request
	bootstrap, err := s.getBootstrapByName(r.Context(), name)
	if err != nil {
		logger.Error(err, "Failed to get Bootstrap object")
		http.Error(w, "Bootstrap object not found", http.StatusNotFound)
		return
	}

	// Validate webhook configuration
	if bootstrap.Spec.Webhook == nil || !bootstrap.Spec.Webhook.Enabled {
		logger.Info("Webhook not enabled for Bootstrap object")
		http.Error(w, "Webhook not enabled", http.StatusBadRequest)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error(err, "Failed to read request body")
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Authenticate the request
	if err := s.authenticateRequest(r.Context(), bootstrap, r.Header, body); err != nil {
		logger.Error(err, "Authentication failed")
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	// Parse webhook payload
	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logger.Error(err, "Failed to parse webhook payload")
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Validate payload based on source type
	if !s.validatePayload(bootstrap, payload) {
		logger.Info("Payload validation failed")
		http.Error(w, "Payload validation failed", http.StatusBadRequest)
		return
	}

	// Trigger reconciliation
	s.triggerReconciliation(bootstrap.Namespace, bootstrap.Name)

	logger.Info("Webhook processed successfully")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleHealth provides a health check endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// getBootstrapByName retrieves a Bootstrap object by searching all namespaces.
func (s *Server) getBootstrapByName(ctx context.Context, name string) (*v1alpha1.Bootstrap, error) {
	bootstrapList := &v1alpha1.BootstrapList{}
	if err := s.client.List(ctx, bootstrapList); err != nil {
		return nil, fmt.Errorf("failed to list Bootstrap objects: %w", err)
	}

	for _, bootstrap := range bootstrapList.Items {
		if bootstrap.Name == name {
			return &bootstrap, nil
		}
	}

	return nil, fmt.Errorf("Bootstrap object %s not found", name)
}

// authenticateRequest validates the webhook request authentication.
func (s *Server) authenticateRequest(ctx context.Context, bootstrap *v1alpha1.Bootstrap, headers http.Header, body []byte) error {
	webhookConfig := bootstrap.Spec.Webhook
	if webhookConfig.Secret == nil && len(webhookConfig.Headers) == 0 {
		return nil // No authentication required
	}

	// Validate required headers
	for key, expectedValue := range webhookConfig.Headers {
		actualValue := headers.Get(key)
		if actualValue != expectedValue {
			return fmt.Errorf("header %s mismatch", key)
		}
	}

	// Validate HMAC signature if secret is configured
	if webhookConfig.Secret != nil {
		return s.validateHMACSignature(ctx, bootstrap, headers, body)
	}

	return nil
}

// validateHMACSignature validates the HMAC signature of the webhook request.
func (s *Server) validateHMACSignature(ctx context.Context, bootstrap *v1alpha1.Bootstrap, headers http.Header, body []byte) error {
	secretConfig := bootstrap.Spec.Webhook.Secret
	secretNamespace := secretConfig.Namespace
	if secretNamespace == "" {
		secretNamespace = bootstrap.Namespace
	}

	// Get the secret
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      secretConfig.Name,
		Namespace: secretNamespace,
	}, secret); err != nil {
		return fmt.Errorf("failed to get webhook secret: %w", err)
	}

	// Get the secret key
	secretKey := secretConfig.SecretKey
	if secretKey == "" {
		secretKey = "secret"
	}

	secretValue, exists := secret.Data[secretKey]
	if !exists {
		return fmt.Errorf("secret key %s not found in secret", secretKey)
	}

	// Get signature from headers (GitHub style)
	signature := headers.Get("X-Hub-Signature-256")
	if signature == "" {
		signature = headers.Get("X-Gitlab-Token")
	}
	if signature == "" {
		return fmt.Errorf("no signature found in headers")
	}

	// Validate HMAC
	mac := hmac.New(sha256.New, secretValue)
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// Remove "sha256=" prefix if present
	if strings.HasPrefix(signature, "sha256=") {
		signature = signature[7:]
	}

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return fmt.Errorf("HMAC signature validation failed")
	}

	return nil
}

// validatePayload validates the webhook payload based on the source type.
func (s *Server) validatePayload(bootstrap *v1alpha1.Bootstrap, payload WebhookPayload) bool {
	source := bootstrap.Spec.Source

	switch {
	case source.GitHub != nil:
		return s.validateGitHubPayload(source.GitHub, payload)
	case source.GitLab != nil:
		return s.validateGitLabPayload(source.GitLab, payload)
	case source.Helm != nil:
		return s.validateHelmPayload(source.Helm, payload)
	default:
		return false
	}
}

// validateGitHubPayload validates GitHub webhook payload.
func (s *Server) validateGitHubPayload(github *v1alpha1.GitHub, payload WebhookPayload) bool {
	expectedRepo := fmt.Sprintf("%s/%s", github.Owner, github.Repo)
	return payload.Repository.FullName == expectedRepo && payload.Action == "published"
}

// validateGitLabPayload validates GitLab webhook payload.
func (s *Server) validateGitLabPayload(gitlab *v1alpha1.GitLab, payload WebhookPayload) bool {
	expectedRepo := fmt.Sprintf("%s/%s", gitlab.Owner, gitlab.Repo)
	return payload.Repository.FullName == expectedRepo
}

// validateHelmPayload validates Helm webhook payload.
func (s *Server) validateHelmPayload(helm *v1alpha1.Helm, payload WebhookPayload) bool {
	return payload.Repository.Name == helm.ChartName
}

// triggerReconciliation triggers a reconciliation for the specified Bootstrap object.
func (s *Server) triggerReconciliation(namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	if ch, exists := s.triggers[key]; exists {
		select {
		case ch <- struct{}{}:
		default:
			// Channel is full, reconciliation already pending
		}
	}
}