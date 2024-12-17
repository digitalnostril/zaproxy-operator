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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v2"
	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
)

// Definitions to manage status conditions
const (
	// typeAvailableZAProxy represents the status of the Deployment reconciliation
	typeAvailableZAProxy = "Available"
	// typeDegradedZAProxy represents the status used when the custom resource is deleted and the finalizer operations are must to occur.
	typeDegradedZAProxy = "Degraded"
	zaproxyFinalizer    = "finalizer.zaproxy.org"
)

// ZAProxyReconciler reconciles a ZAProxy object
type ZAProxyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// The following markers are used to generate the rules permissions (RBAC) on config/rbac using controller-gen
// when the command <make manifests> is executed.
// To know more about markers see: https://book.kubebuilder.io/reference/markers.html

//+kubebuilder:rbac:groups=zaproxy.org,resources=zaproxies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=zaproxy.org,resources=zaproxies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=zaproxy.org,resources=zaproxies/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main Kubernetes reconciliation loop. It compares the desired state
// specified by the ZAProxy object against the actual cluster state. It then performs operations
// to align the cluster state with the desired state. Specifically, it ensures that a ConfigMap
// and a PersistentVolumeClaim (PVC) exist for each ZAProxy.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ZAProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Starting reconciliation", "Namespace", req.NamespacedName.Namespace, "Name", req.NamespacedName.Name)

	zaproxy := &zaproxyorgv1alpha1.ZAProxy{}
	err := r.Get(ctx, req.NamespacedName, zaproxy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("zaproxy resource not found. Ignoring since object must be deleted", "Namespace", req.NamespacedName.Namespace, "Name", req.NamespacedName.Name)
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get zaproxy %v: %w", req.NamespacedName, err)
	}

	if zaproxy.GetDeletionTimestamp() != nil {
		if containsString(zaproxy.GetFinalizers(), zaproxyFinalizer) {
			// Run finalization logic for zaproxyFinalizer. If the finalization logic fails, don't remove the finalizer so that we can retry during the next reconciliation.
			if err := r.finalizeZAProxy(ctx, zaproxy); err != nil {
				return ctrl.Result{}, err
			}

			// Remove zaproxyFinalizer. Once all finalizers have been removed, the object will be deleted.
			zaproxy.SetFinalizers(removeString(zaproxy.GetFinalizers(), zaproxyFinalizer))
			if err := r.Update(ctx, zaproxy); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !containsString(zaproxy.GetFinalizers(), zaproxyFinalizer) {
		zaproxy.SetFinalizers(append(zaproxy.GetFinalizers(), zaproxyFinalizer))
		if err := r.Update(ctx, zaproxy); err != nil {
			return ctrl.Result{}, err
		}
	}

	if zaproxy.Status.HasNoStatus() {
		meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{Type: typeAvailableZAProxy, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, zaproxy); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update ZAProxy status %v: %w", req.NamespacedName, err)
		}

		// Let's re-fetch the zaproxy Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, zaproxy); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to re-fetch zaproxy %v: %w", req.NamespacedName, err)
		}
	}

	if err := r.reconcileConfigMap(ctx, zaproxy); err != nil {
		return ctrl.Result{}, r.setReconcileError(ctx, zaproxy, err, fmt.Sprintf("failed to reconcile ConfigMap %v", req.NamespacedName))
	}

	if err := r.reconcilePVC(ctx, zaproxy); err != nil {
		return ctrl.Result{}, r.setReconcileError(ctx, zaproxy, err, fmt.Sprintf("failed to reconcile PVC %v", req.NamespacedName))
	}

	if err := r.reconcileService(ctx, zaproxy); err != nil {
		return ctrl.Result{}, r.setReconcileError(ctx, zaproxy, err, fmt.Sprintf("failed to reconcile Service %v", req.NamespacedName))
	}

	log.Info("Reconciliation Complete", "Namespace", req.NamespacedName.Namespace, "Name", req.NamespacedName.Name)

	return ctrl.Result{}, r.setReconcileSuccess(ctx, zaproxy, fmt.Sprintf("Reconciled successfully %v", req.NamespacedName))
}

// setReconcileSuccess sets the status of the ZAProxy resource to success
func (r *ZAProxyReconciler) setReconcileSuccess(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy, message string) error {
	meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{
		Type:    typeAvailableZAProxy,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciled",
		Message: message,
	})

	if updateErr := r.Status().Update(ctx, zaproxy); updateErr != nil {
		return fmt.Errorf("failed to update ZAProxy status %s/%s: %w", zaproxy.Namespace, zaproxy.Name, updateErr)
	}

	return nil
}

// setReconcileError sets the status of the ZAProxy resource to error
func (r *ZAProxyReconciler) setReconcileError(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy, err error, message string) error {
	meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{
		Type:    typeAvailableZAProxy,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})

	if updateErr := r.Status().Update(ctx, zaproxy); updateErr != nil {
		return fmt.Errorf("failed to update ZAProxy status %s/%s: %w", zaproxy.Namespace, zaproxy.Name, updateErr)
	}

	return fmt.Errorf(message+": %w", err)
}

// reconcileConfigMap reconciles the ConfigMap for the ZAProxy resource
func (r *ZAProxyReconciler) reconcileConfigMap(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy) error {
	log := log.FromContext(ctx)

	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-config", Namespace: zaproxy.Namespace}, configMap)

	if err != nil && apierrors.IsNotFound(err) {

		configMap, err = r.configMapForZAProxy(zaproxy)
		if err != nil {
			return fmt.Errorf("failed to define a new ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		if err := r.Create(ctx, configMap); err != nil {
			return fmt.Errorf("failed to create new ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}

		log.Info("Successfully created new ConfigMap", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)

		return nil

	} else if err != nil {
		return fmt.Errorf("failed to get ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
	} else {
		// ConfigMap was found, check if it needs to be updated
		updatedConfigMap, err := r.configMapForZAProxy(zaproxy)
		if err != nil {
			return fmt.Errorf("failed to define updated ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}

		if !reflect.DeepEqual(configMap.Data, updatedConfigMap.Data) {
			// The current state is different from the desired state, update the ConfigMap
			configMap.Data = updatedConfigMap.Data
			if err := r.Update(ctx, configMap); err != nil {
				return fmt.Errorf("failed to update ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
			}
			log.Info("Successfully updated ConfigMap", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)
		}
	}

	return nil
}

// reconcilePVC reconciles the PVC for the ZAProxy resource
func (r *ZAProxyReconciler) reconcilePVC(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy) error {
	log := log.FromContext(ctx)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-pvc", Namespace: zaproxy.Namespace}, pvc)

	if err != nil && apierrors.IsNotFound(err) {

		pvc, err = r.pvcForZAProxy(zaproxy)
		if err != nil {
			return fmt.Errorf("failed to define a new PVC %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		if err := r.Create(ctx, pvc); err != nil {
			return fmt.Errorf("failed to create new PVC %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}

		log.Info("Successfully created new PVC", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)

		return nil

	} else if err != nil {
		return fmt.Errorf("failed to get PVC %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
	}

	return nil
}

func (r *ZAProxyReconciler) reconcileService(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy) error {
	log := log.FromContext(ctx)

	service := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: zaproxy.Name, Namespace: zaproxy.Namespace}, service)

	if err != nil && apierrors.IsNotFound(err) {

		service, err = r.serviceForZAProxy(zaproxy)
		if err != nil {
			return fmt.Errorf("failed to define a new Service %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		if err := r.Create(ctx, service); err != nil {
			return fmt.Errorf("failed to create new Service %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}

		log.Info("Successfully created new Service", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)

		return nil

	} else if err != nil {
		return fmt.Errorf("failed to get Service %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
	}

	return nil
}

// configMapForZAProxy returns a ZAProxy ConfigMap object
func (r *ZAProxyReconciler) configMapForZAProxy(zaproxy *zaproxyorgv1alpha1.ZAProxy) (*corev1.ConfigMap, error) {
	var plan map[string]interface{}
	if err := json.Unmarshal(zaproxy.Spec.Automation.Plan.Raw, &plan); err != nil {
		return nil, err
	}

	planStr, err := yaml.Marshal(plan)
	if err != nil {
		return nil, err
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zaproxy.Name + "-config",
			Namespace: zaproxy.Namespace,
			Labels:    labelsForZAProxy(zaproxy.Name),
		},
		Data: map[string]string{
			"af-plan.yaml": string(planStr),
		},
	}

	if err := ctrl.SetControllerReference(zaproxy, configMap, r.Scheme); err != nil {
		return nil, err
	}
	return configMap, nil
}

// pvcForZAProxy returns a ZAProxy PersistentVolumeClaim object
func (r *ZAProxyReconciler) pvcForZAProxy(zaproxy *zaproxyorgv1alpha1.ZAProxy) (*corev1.PersistentVolumeClaim, error) {

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zaproxy.Name + "-pvc",
			Namespace: zaproxy.Namespace,
			Labels:    labelsForZAProxy(zaproxy.Name),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
			StorageClassName: &zaproxy.Spec.StorageClassName,
		},
	}

	if err := ctrl.SetControllerReference(zaproxy, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}

func (r *ZAProxyReconciler) serviceForZAProxy(zaproxy *zaproxyorgv1alpha1.ZAProxy) (*corev1.Service, error) {

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labelsForZAProxy(zaproxy.Name),
			Annotations: make(map[string]string),
			Name:        zaproxy.Name,
			Namespace:   zaproxy.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labelsForZAProxy(zaproxy.Name),
			Ports: []corev1.ServicePort{
				{
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
					Name:       "zaproxy",
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(zaproxy, service, r.Scheme); err != nil {
		return nil, err
	}

	return service, nil
}

// imageForZAProxy gets the Operand image which is managed by this controller
// from the ZAProxy_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForZAProxy() (string, error) {
	var imageEnvVar = "ZAPROXY_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		return "", fmt.Errorf("unable to find %s environment variable with the image", imageEnvVar)
	}
	return image, nil
}

// labelsForZAProxy returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForZAProxy(name string) map[string]string {
	var imageTag string
	image, err := imageForZAProxy()
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	return map[string]string{"app.kubernetes.io/name": "ZAProxy",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "zaproxy-operator",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZAProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zaproxyorgv1alpha1.ZAProxy{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func (r *ZAProxyReconciler) finalizeZAProxy(ctx context.Context, zaproxy *zaproxyorgv1alpha1.ZAProxy) error {
	log := log.FromContext(ctx)
	log.Info("Finalizing ZAProxy", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)

	// Delete associated ConfigMap
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-config", Namespace: zaproxy.Namespace}, cm)
	if err == nil {
		if err := r.Delete(ctx, cm); err != nil {
			return fmt.Errorf("failed to delete ConfigMap %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		log.Info("Successfully deleted ConfigMap", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)
	}

	// Delete associated PVC
	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-pvc", Namespace: zaproxy.Namespace}, pvc)
	if err == nil {
		if err := r.Delete(ctx, pvc); err != nil {
			return fmt.Errorf("failed to delete PVC %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		log.Info("Successfully deleted PVC", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)
	}

	// Delete associated Service
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name, Namespace: zaproxy.Namespace}, service)
	if err == nil {
		if err := r.Delete(ctx, service); err != nil {
			return fmt.Errorf("failed to delete Service %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		log.Info("Successfully deleted Service", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)
	}

	// Delete associated Jobs
	job := &kbatch.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name, Namespace: zaproxy.Namespace}, job)
	if err == nil {
		if err := r.Delete(ctx, job); err != nil {
			return fmt.Errorf("failed to delete Job %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
		}
		log.Info("Successfully deleted Job", "Namespace", zaproxy.Namespace, "Name", zaproxy.Name)
	}

	jobList := &kbatch.JobList{}
	listOpts := []client.ListOption{
		client.InNamespace(zaproxy.Namespace),
		client.MatchingLabels(labelsForZAProxy(zaproxy.Name)),
	}
	if err := r.List(ctx, jobList, listOpts...); err != nil {
		return fmt.Errorf("failed to list Jobs for ZAProxy %s/%s: %w", zaproxy.Namespace, zaproxy.Name, err)
	}

	for _, job := range jobList.Items {
		if err := r.Delete(ctx, &job); err != nil {
			return fmt.Errorf("failed to delete Job %s/%s: %w", job.Namespace, job.Name, err)
		}
		log.Info("Successfully deleted Job", "Namespace", job.Namespace, "Name", job.Name)
	}

	return nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
