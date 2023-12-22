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

func CreateJob(name string, namespace string, c client.Client) (ctrl.Result, error) {
	ctx := context.TODO()
	log := log.FromContext(ctx)

	namespacedName := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	zaproxy := &zaproxyorgv1alpha1.ZAProxy{}
	err := c.Get(ctx, namespacedName, zaproxy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("zaproxy resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get zaproxy")
		return ctrl.Result{}, err
	}

	// Get the Operand image
	image, err := imageForZAProxy()
	if err != nil {
		return ctrl.Result{}, err
	}

	constructJob := func(zaproxy *zaproxyorgv1alpha1.ZAProxy, scheduledTime time.Time) (*kbatch.Job, error) {
		// We want job names for a given nominal start time to have a deterministic name to avoid the same job being created twice
		name := fmt.Sprintf("%s-%d", zaproxy.Name, scheduledTime.Unix())

		job := &kbatch.Job{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
				Name:        name,
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
								Args: []string{"./zap.sh", "-cmd", "-autorun", "/zap/config/af-plan.yaml"},
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
	// +kubebuilder:docs-gen:collapse=constructJobForCronJob

	log.Info("Starting things up", "namespace", namespacedName.String())

	// actually make the job...
	job, err := constructJob(zaproxy, time.Now())
	if err != nil {
		log.Error(err, "unable to construct job from template")
		// don't bother requeuing until we get a change to the spec
		return ctrl.Result{}, nil
	}

	// ...and create it on the cluster
	if err := c.Create(ctx, job); err != nil {
		log.Error(err, "unable to create Job for ZAProxy", "job", job)
		return ctrl.Result{}, err
	}

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

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: zaproxy.Name, Namespace: zaproxy.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		dep, err := r.deploymentForZAProxy(zaproxy)
		if err != nil {
			log.Error(err, "Failed to define new Deployment resource for ZAProxy")

			// The following implementation will update the status
			meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{Type: typeAvailableZAProxy,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", zaproxy.Name, err)})

			if err := r.Status().Update(ctx, zaproxy); err != nil {
				log.Error(err, "Failed to update ZAProxy status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// The CRD API is defining that the ZAProxy type, have a ZAProxySpec.Size field
	// to set the quantity of Deployment instances is the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := zaproxy.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the zaproxy Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, zaproxy); err != nil {
				log.Error(err, "Failed to re-fetch zaproxy")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{Type: typeAvailableZAProxy,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", zaproxy.Name, err)})

			if err := r.Status().Update(ctx, zaproxy); err != nil {
				log.Error(err, "Failed to update ZAProxy status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	// log.Info("Let's try to change the replica count")
	// result, err := UpdateDeploy(req, r.Client)
	// if err != nil {

	// 	log.Error(err, "Failed to update replica count")
	// 	return result, err
	// }

	// The following implementation will update the status
	meta.SetStatusCondition(&zaproxy.Status.Conditions, metav1.Condition{Type: typeAvailableZAProxy,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", zaproxy.Name, size)})

	if err := r.Status().Update(ctx, zaproxy); err != nil {
		log.Error(err, "Failed to update ZAProxy status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// deploymentForZAProxy returns a ZAProxy Deployment object
func (r *ZAProxyReconciler) deploymentForZAProxy(
	zaproxy *zaproxyorgv1alpha1.ZAProxy) (*appsv1.Deployment, error) {
	ls := labelsForZAProxy(zaproxy.Name)
	replicas := zaproxy.Spec.Size

	// Get the Operand image
	image, err := imageForZAProxy()
	if err != nil {
		return nil, err
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zaproxy.Name,
			Namespace: zaproxy.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "zaproxy",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							// WARNING: Ensure that the image used defines an UserID in the Dockerfile
							// otherwise the Pod will not run and will fail with "container has runAsNonRoot and image has non-numeric user"".
							// If you want your workloads admitted in namespaces enforced with the restricted mode in OpenShift/OKD vendors
							// then, you MUST ensure that the Dockerfile defines a User ID OR you MUST leave the "RunAsNonRoot" and
							// "RunAsUser" fields empty.
							RunAsNonRoot: &[]bool{true}[0],
							// The zaproxy image does not use a non-zero numeric user as the default user.
							// Due to RunAsNonRoot field being set to true, we need to force the user in the
							// container to a non-zero numeric user. We do this using the RunAsUser field.
							// However, if you are looking to provide solution for K8s vendors like OpenShift
							// be aware that you cannot run under its restricted-v2 SCC if you set this value.
							RunAsUser:                &[]int64{1001}[0],
							AllowPrivilegeEscalation: &[]bool{false}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: zaproxy.Spec.ContainerPort,
							Name:          "zaproxy",
						}},
						//Args: []string{"zap-webswing.sh"},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(zaproxy, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *ZAProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zaproxyorgv1alpha1.ZAProxy{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
