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

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/fluxcd/pkg/ssa"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/Skarlso/crd-bootstrap/api/v1alpha1"
	"github.com/Skarlso/crd-bootstrap/pkg/source"
)

const (
	finalizer = "delivery.crd-bootstrap"
)

// BootstrapReconciler reconciles a Bootstrap object.
type BootstrapReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	SourceProvider        source.Contract
	DefaultServiceAccount string
	WebhookTriggers       map[string]<-chan struct{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootstrapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Bootstrap{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps/finalizers,verbs=update
//+kubebuilder:rbac:groups=delivery.crd-bootstrap,resources=bootstraps/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BootstrapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	obj := &v1alpha1.Bootstrap{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	if obj.DeletionTimestamp != nil {
		if !controllerutil.ContainsFinalizer(obj, finalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.reconcileDelete(ctx, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete bootstrap: %w", err)
		}

		return ctrl.Result{}, nil
	}

	logger.Info("starting reconcile loop")

	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// AddFinalizer if not present already.
	controllerutil.AddFinalizer(obj, finalizer)

	// Always attempt to patch the object and status after each reconciliation.
	defer func() {
		// Patching has not been set up, or the controller errored earlier.
		if patchHelper == nil {
			return
		}

		obj.Status.ObservedGeneration = obj.Generation

		// Set status observed generation option if the object is stalled or ready.
		if perr := patchHelper.Patch(ctx, obj); perr != nil {
			err = errors.Join(err, perr)
		}
	}()

	// Check if webhook is enabled and wait for webhook trigger
	if obj.Spec.Webhook != nil && obj.Spec.Webhook.Enabled {
		key := fmt.Sprintf("%s/%s", obj.Namespace, obj.Name)
		if triggerCh, exists := r.WebhookTriggers[key]; exists {
			logger.Info("waiting for webhook trigger...")
			select {
			case <-triggerCh:
				logger.Info("webhook trigger received, proceeding with reconciliation")
			case <-ctx.Done():
				return ctrl.Result{}, ctx.Err()
			default:
				// No webhook trigger yet, use fallback interval if configured
				if obj.Spec.Interval.Duration > 0 {
					logger.Info("no webhook trigger, using fallback interval")
				} else {
					logger.Info("no webhook trigger and no fallback interval, skipping reconciliation")
					conditions.MarkTrue(obj, meta.ReadyCondition, meta.SucceededReason, "Waiting for webhook trigger")
					return ctrl.Result{RequeueAfter: obj.GetRequeueAfter()}, nil
				}
			}
		}
	}

	update, revision, err := r.SourceProvider.HasUpdate(ctx, obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check version: %w", err)
	}

	if !update {
		logger.Info("no update was required...")
		conditions.MarkTrue(obj, meta.ReadyCondition, meta.SucceededReason, "Successfully applied crd(s)")

		// If webhook is enabled, don't requeue unless fallback interval is set
		if obj.Spec.Webhook != nil && obj.Spec.Webhook.Enabled && obj.Spec.Interval.Duration == 0 {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{RequeueAfter: obj.GetRequeueAfter()}, nil
	}

	logger.Info("fetching CRD content")

	obj.Status.LastAttemptedRevision = revision

	temp, err := os.MkdirTemp("", "crd")
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, "TempFolderFailedToCreate", "failed to create temp directory: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// should probably return a file system / single YAML. Because they can be super large, it's
	// not vise to store it in memory as a buffer.
	location, err := r.SourceProvider.FetchCRD(ctx, temp, obj, revision)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, "CRDFetchFailed", "failed to fetch source: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to fetch source: %w", err)
	}

	defer func() {
		if oerr := os.RemoveAll(temp); oerr != nil {
			err = errors.Join(err, oerr)
		}
	}()

	sm, err := r.NewResourceManager(ctx, obj)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, "ResourceManagerCreateFailed", "failed to create resource manager: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to create resource manager: %w", err)
	}

	objects, err := readObjects(location)
	if err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, "ReadingObjectsToApplyFailed", "failed to construct objects to apply: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to construct objects to apply: %w", err)
	}

	applied := obj.Status.LastAppliedCRDNames
	if applied == nil {
		applied = make(map[string]int)
	}

	for _, o := range objects {
		o.SetLabels(map[string]string{
			v1alpha1.BootstrapOwnerLabelKey: obj.GetName(),
		})

		applied[o.GetName()]++
	}

	if err := r.validateObjects(ctx, obj, objects); err != nil {
		if !obj.Spec.ContinueOnValidationError {
			conditions.MarkFalse(obj, meta.ReadyCondition, "CRDValidationFailed", "validation failed to on the crd template: %s", err)
			logger.Error(err, "validation failed to the CRD for the provided template")

			return ctrl.Result{}, err
		}

		logger.Error(err, "validation failed for the CRD, but continue is set so we'll ignore this error")
	}

	if _, err := sm.ApplyAllStaged(ctx, objects, ssa.DefaultApplyOptions()); err != nil {
		err := fmt.Errorf("failed to apply manifests: %w", err)
		conditions.MarkFalse(obj, meta.ReadyCondition, "ApplyingCRDSFailed", "failed to apply all stages: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to apply all stages: %w", err)
	}

	if err = sm.Wait(objects, ssa.DefaultWaitOptions()); err != nil {
		err := fmt.Errorf("failed to wait for objects to be ready: %w", err)
		conditions.MarkFalse(obj, meta.ReadyCondition, "WaitingOnObjectsFailed", "failed to wait for applied objects: %s", err)

		return ctrl.Result{}, fmt.Errorf("failed to wait for applied objects: %w", err)
	}

	obj.Status.LastAppliedCRDNames = applied
	obj.Status.LastAppliedRevision = revision

	conditions.MarkTrue(obj, meta.ReadyCondition, meta.SucceededReason, "Successfully applied crd(s)")

	logger.Info("all done")

	// If webhook is enabled, don't requeue unless fallback interval is set
	if obj.Spec.Webhook != nil && obj.Spec.Webhook.Enabled && obj.Spec.Interval.Duration == 0 {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: obj.GetRequeueAfter()}, nil
}

func (r *BootstrapReconciler) reconcileDelete(ctx context.Context, obj *v1alpha1.Bootstrap) error {
	patchHelper := patch.NewSerialPatcher(obj, r.Client)

	// don't delete anything if prune is not set.
	if !obj.Spec.Prune {
		controllerutil.RemoveFinalizer(obj, finalizer)

		return patchHelper.Patch(ctx, obj)
	}

	logger := log.FromContext(ctx)
	logger.Info("cleaning owned CRDS...")

	crds := &v1.CustomResourceDefinitionList{}
	if err := r.List(ctx, crds, client.MatchingLabels(map[string]string{
		v1alpha1.BootstrapOwnerLabelKey: obj.GetName(),
	})); err != nil {
		return fmt.Errorf("failed to list owned CRDS: %w", err)
	}

	logger.Info("found number of crds to clean", "number", len(crds.Items))

	for _, item := range crds.Items {
		logger.V(v1alpha1.LogLevelDebug).Info("removed CRD", "crd", item.GetName())
		if err := r.Delete(ctx, &item); err != nil {
			return fmt.Errorf("failed to delete object with name %s: %w", item.GetName(), err)
		}
	}

	controllerutil.RemoveFinalizer(obj, finalizer)

	return patchHelper.Patch(ctx, obj)
}

func (r *BootstrapReconciler) validateObjects(ctx context.Context, obj *v1alpha1.Bootstrap, objects []*unstructured.Unstructured) error {
	// bail early if there are no templates.
	if obj.Spec.Template == nil {
		return nil
	}

	logger := log.FromContext(ctx)

	for _, o := range objects {
		logger.Info("validating the following object against set template data", "name", o.GetName())
		// Create a CRD out of the content.
		content, err := o.MarshalJSON()
		if err != nil {
			return err
		}

		crd := &apiextensions.CustomResourceDefinition{}
		if err := yaml.Unmarshal(content, crd); err != nil {
			return errors.New("failed to unmarshal into custom resource definition")
		}

		// Add checking out the api version from the provided template and only eval against that.
		for _, v := range crd.Spec.Versions {
			eval, _, err := validation.NewSchemaValidator(v.Schema.OpenAPIV3Schema)
			if err != nil {
				return err
			}

			if v, ok := obj.Spec.Template[crd.Spec.Names.Kind]; ok {
				if err := eval.Validate(v).AsError(); err != nil {
					return fmt.Errorf("failed to validate kind %s: %w", crd.Spec.Names.Kind, err)
				}
			}
		}
	}

	return nil
}
