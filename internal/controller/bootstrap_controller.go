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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Skarlso/crd-bootstrap/pkg/source"
	"github.com/fluxcd/pkg/ssa"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
)

// BootstrapReconciler reconciles a Bootstrap object
type BootstrapReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	SourceProvider source.Contract
}

//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BootstrapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	logger.Info("replication going")

	obj := &v1alpha1.Bootstrap{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	temp, err := os.MkdirTemp("", "crd")
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temp folder: %w", err)
	}

	// should probably return a file system / single YAML. Because they can be super large, it's
	// not vise to store it in memory as a buffer.
	crd, err := r.SourceProvider.FetchCRD(temp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch source: %w", err)
	}

	defer func() {
		if oerr := os.RemoveAll(temp); oerr != nil {
			err = errors.Join(err, oerr)
		}
	}()

	kubeconfigArgs := genericclioptions.NewConfigFlags(false)
	sm, err := NewResourceManager(kubeconfigArgs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("✗ failed to create resource manager: %w", err)
	}

	objects, err := readObjects(crd)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("✗ failed to construct objects to apply: %w", err)
	}

	if _, err := sm.ApplyAllStaged(context.Background(), objects, ssa.DefaultApplyOptions()); err != nil {
		return ctrl.Result{}, fmt.Errorf("✗ failed to apply manifests: %w", err)
	}

	logger.Info("waiting for ocm deployment to be ready")
	if err = sm.Wait(objects, ssa.DefaultWaitOptions()); err != nil {
		return ctrl.Result{}, fmt.Errorf("✗ failed to wait for objects to be ready: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootstrapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Bootstrap{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
