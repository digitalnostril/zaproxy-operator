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
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
)

func EndDelayZAPJob(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	job, err := getJob(ctx, c, namespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}

	ip, err := getJobPodIP(ctx, c, job)
	if err != nil {
		return ctrl.Result{}, err
	}

	port, err := getJobContainerPort(job)
	if err != nil {
		return ctrl.Result{}, err
	}

	uri := fmt.Sprintf("http://%s:%s/JSON/automation/action/endDelayJob", ip, strconv.Itoa(int(port)))
	log.Info("Sending request", "URI", uri, "Job", job.Name, "Namespace", job.Namespace)

	resp, err := sendHTTPRequest(ctx, "GET", uri, time.Second*60)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ctrl.Result{}, fmt.Errorf("received non-OK status code %d from URL %s", resp.StatusCode, resp.Request.URL)
	}

	log.Info("Received response", "Status Code", resp.StatusCode, "Status", resp.Status, "URL", resp.Request.URL, "Job", job.Name, "Namespace", job.Namespace)
	return ctrl.Result{}, nil
}

func CreateJob(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	zaproxy := &zaproxyorgv1alpha1.ZAProxy{}
	if err := c.Get(ctx, namespacedName, zaproxy); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get zaproxy resource %v: %w", namespacedName, err)
	}

	image, err := imageForZAProxy()
	if err != nil {
		return ctrl.Result{}, err
	}

	jobName := getJobName(namespacedName)

	job, err := getJob(ctx, c, namespacedName)

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error occurred retrieving job %v: %w", namespacedName, err)
		}
	} else if isJobFinished(ctx, job) {
		if err := deleteJob(ctx, c, job); err != nil {
			return ctrl.Result{}, err
		}
	}

	job, err = constructJob(c, zaproxy, jobName, image)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to construct job from template %v: %w", namespacedName, err)
	}

	if err := c.Create(ctx, job); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to create Job for ZAProxy %v: %w", namespacedName, err)
	}

	log.Info("Job created successfully", "Job.Namespace", job.Namespace, "Job.Name", job.Name)

	return ctrl.Result{}, nil
}

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
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ZAProxy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ZAProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the ZAProxy instance
	zaproxy := &zaproxyorgv1alpha1.ZAProxy{}
	err := r.Get(ctx, req.NamespacedName, zaproxy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("zaproxy resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get zaproxy")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status are available
	if zaproxy.Status.Conditions == nil || len(zaproxy.Status.Conditions) == 0 {
		meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{Type: typeAvailableZAProxy, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, zaproxy); err != nil {
			log.Error(err, "Failed to update ZAProxy status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the zaproxy Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, zaproxy); err != nil {
			log.Error(err, "Failed to re-fetch zaproxy")
			return ctrl.Result{}, err
		}
	}

	// Check if the ConfigMap already exists, if not create a new one
	configMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-config", Namespace: zaproxy.Namespace}, configMap)

	if err != nil {
		log.Info("Get ConfigMap returned an error", "error", err)
	}

	if err != nil && apierrors.IsNotFound(err) {
		// Define a new ConfigMap
		configMap, err = r.configMapForZAProxy(zaproxy)
		if err != nil {
			log.Error(err, "Failed to define a new ConfigMap", "ConfigMap.Namespace", zaproxy.Namespace, "ConfigMap.Name", zaproxy.Name+"-config")
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, configMap); err != nil {
			log.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			return ctrl.Result{}, err
		}
		// ConfigMap created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else {
		log.Info("Get ConfigMap did not return an error, ConfigMap found", "ConfigMap", configMap)
	}

	// Check if the PVC already exists, if not create a new one
	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name + "-pvc", Namespace: zaproxy.Namespace}, pvc)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new PVC
		pvc = r.pvcForZAProxy(zaproxy)
		if err := r.Create(ctx, pvc); err != nil {
			log.Error(err, "Failed to create new PVC", "PVC.Namespace", pvc.Namespace, "PVC.Name", pvc.Name)
			return ctrl.Result{}, err
		}
		// PVC created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
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

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zaproxy.Name + "-config",
			Namespace: zaproxy.Namespace,
		},
		Data: map[string]string{
			"af-plan.yaml": string(planStr),
		},
	}, nil
}

// pvcForZAProxy returns a ZAProxy PersistentVolumeClaim object
func (r *ZAProxyReconciler) pvcForZAProxy(zaproxy *zaproxyorgv1alpha1.ZAProxy) *corev1.PersistentVolumeClaim {
	log := log.FromContext(context.TODO())

	storageClassName := zaproxy.Spec.StorageClassName
	log.Info("StorageClassName", "storageClassName", storageClassName)

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zaproxy.Name + "-pvc",
			Namespace: zaproxy.Namespace,
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
			StorageClassName: &storageClassName,
		},
	}
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

// imageForZAProxy gets the Operand image which is managed by this controller
// from the ZAProxy_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForZAProxy() (string, error) {
	var imageEnvVar = "ZAPROXY_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		return "", fmt.Errorf("Unable to find %s environment variable with the image", imageEnvVar)
	}
	return image, nil
}

func constructJob(c client.Client, zaproxy *zaproxyorgv1alpha1.ZAProxy, jobName string, image string) (*kbatch.Job, error) {

	job := &kbatch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
			Name:        jobName,
			Namespace:   zaproxy.Namespace,
		},
		Spec: kbatch.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      make(map[string]string),
					Annotations: make(map[string]string),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "zaproxy",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Name:          "zaproxy",
								},
							},
							Args: []string{"./zap.sh", "-cmd", "-autorun", "/zap/config/af-plan.yaml", "-host", "0.0.0.0", "-config", "api.disablekey=true", "-config", "api.addrs.addr.name=.*", "-config", "api.addrs.addr.regex=true"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/zap/config",
								},
								{
									Name:      "pvc",
									MountPath: "/zap/reports",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: zaproxy.Name + "-config",
									},
								},
							},
						},
						{
							Name: "pvc",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: zaproxy.Name + "-pvc",
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(zaproxy, job, c.Scheme()); err != nil {
		return nil, err
	}

	return job, nil
}

// +kubebuilder:docs-gen:collapse=constructJob

func getJobName(namespacedName types.NamespacedName) string {
	return namespacedName.Name + "-job"
}

func getJob(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (*kbatch.Job, error) {

	jobName := getJobName(namespacedName)
	job := &kbatch.Job{}
	err := c.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespacedName.Namespace}, job)

	if err != nil {
		return nil, fmt.Errorf("failed to get job with name %s in namespace %s: %w", jobName, namespacedName.Namespace, err)
	}

	return job, err
}

func isJobFinished(ctx context.Context, job *kbatch.Job) bool {
	log := log.FromContext(ctx)

	for _, c := range job.Status.Conditions {
		if (c.Type == kbatch.JobComplete || c.Type == kbatch.JobFailed) && c.Status == corev1.ConditionTrue {
			log.Info("Job finished", "job", job)
			return true
		}
	}

	return false
}

func waitForJobDeletion(ctx context.Context, c client.Client, job *kbatch.Job) error {

	deletionCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
	defer cancel()

	for {
		time.Sleep(time.Second)

		err := c.Get(deletionCtx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, job)
		if apierrors.IsNotFound(err) {
			break
		} else if err != nil {
			return fmt.Errorf("failed to get job with name %s in namespace %s: %w", job.Name, job.Namespace, err)
		}

		if deletionCtx.Err() != nil {
			return fmt.Errorf("timed out waiting for deletion of job with name %s in namespace %s: %w", job.Name, job.Namespace, deletionCtx.Err())
		}
	}

	return nil
}

func deleteJob(ctx context.Context, c client.Client, job *kbatch.Job) error {

	if err := c.Delete(ctx, job); err != nil {
		return fmt.Errorf("unable to delete old Job for ZAProxy (Job.Namespace: %s, Job.Name: %s): %w", job.Namespace, job.Name, err)
	}

	if err := waitForJobDeletion(ctx, c, job); err != nil {
		return fmt.Errorf("error waiting for job deletion (Job.Namespace: %s, Job.Name: %s): %w", job.Namespace, job.Name, err)
	}

	return nil
}

func getJobPodIP(ctx context.Context, c client.Client, job *kbatch.Job) (string, error) {

	podList := &corev1.PodList{}
	labelSelector := labels.Set(job.Spec.Selector.MatchLabels).AsSelector()
	if err := c.List(ctx, podList, client.InNamespace(job.Namespace), client.MatchingLabelsSelector{Selector: labelSelector}); err != nil {
		return "", fmt.Errorf("failed to list pods for job %s in namespace %s: %w", job.Name, job.Namespace, err)
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return pod.Status.PodIP, nil
		}
	}

	return "", fmt.Errorf("no running pods found for job %s in namespace %s", job.Name, job.Namespace)
}

func getJobContainerPort(job *kbatch.Job) (int32, error) {
	if len(job.Spec.Template.Spec.Containers) > 0 {
		if len(job.Spec.Template.Spec.Containers[0].Ports) > 0 {
			return job.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort, nil
		}
		return 0, fmt.Errorf("no ports found for the first container in job %s in namespace %s", job.Name, job.Namespace)
	}
	return 0, fmt.Errorf("no containers found in the job %s in namespace %s", job.Name, job.Namespace)
}

func sendHTTPRequest(ctx context.Context, method, uri string, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest(method, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for URI %s: %w", uri, err)
	}

	client := &http.Client{
		Timeout: timeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to URL %s: %w", req.URL, err)
	}

	return resp, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZAProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zaproxyorgv1alpha1.ZAProxy{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
