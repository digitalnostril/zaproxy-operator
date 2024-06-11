package controllers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
)

// EndDelayZAPJob handles requests to end the ZAP automation plan delay job for a ZAProxy resource.
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

// WaitForJobReady waits for a ZAP instance to be ready.
func WaitForJobReady(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	job, err := getJob(ctx, c, namespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := waitForJobReady(ctx, c, job); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Job Ready", "Job.Namespace", job.Namespace, "Job.Name", job.Name)

	return ctrl.Result{}, nil
}

// WaitForJobCompletion waits for a ZAP instance job to be completed.
func WaitForJobCompletion(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	job, err := getJob(ctx, c, namespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := waitForJobCompletion(ctx, c, job); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Job Completed", "Job.Namespace", job.Namespace, "Job.Name", job.Name)

	return ctrl.Result{}, nil
}

// CreateJob creates a ZAP instance for a ZAProxy resource as a Job.
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

// constructJob constructs a Job for a ZAProxy resource.
func constructJob(c client.Client, zaproxy *zaproxyorgv1alpha1.ZAProxy, jobName string, image string) (*kbatch.Job, error) {

	job := &kbatch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labelsForZAProxy(zaproxy.Name),
			Annotations: make(map[string]string),
			Name:        jobName,
			Namespace:   zaproxy.Namespace,
		},
		Spec: kbatch.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labelsForZAProxy(zaproxy.Name),
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
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(8080),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								TimeoutSeconds:      3,
								SuccessThreshold:    1,
								FailureThreshold:    3,
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

// getJobName returns the name of the Job for a ZAProxy resource.
func getJobName(namespacedName types.NamespacedName) string {
	return namespacedName.Name + "-job"
}

// getJob returns the Job for a ZAProxy resource.
func getJob(ctx context.Context, c client.Client, namespacedName types.NamespacedName) (*kbatch.Job, error) {

	jobName := getJobName(namespacedName)
	job := &kbatch.Job{}
	err := c.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespacedName.Namespace}, job)

	if err != nil {
		return nil, fmt.Errorf("failed to get job with name %s in namespace %s: %w", jobName, namespacedName.Namespace, err)
	}

	return job, err
}

// isJobFinished returns true if the Job for a ZAProxy resource has finished.
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

// waitForJobDeletion waits for the Job for a ZAProxy resource to be deleted.
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

// waitForJobReady waits for the Job for a ZAProxy resource to be ready.
func waitForJobReady(ctx context.Context, c client.Client, job *kbatch.Job) error {

	deletionCtx, cancel := context.WithDeadline(ctx, time.Now().Add(5*time.Minute))
	defer cancel()

	for {
		time.Sleep(time.Second)

		if deletionCtx.Err() != nil {
			return fmt.Errorf("timed out waiting for job readiness with job name %s in namespace %s: %w", job.Name, job.Namespace, deletionCtx.Err())
		}

		err := c.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, job)
		if err != nil {
			continue
		}

		if job.Status.Ready != nil && *job.Status.Ready == 0 {
			return nil
		}
	}
}

// waitForJobCompletion waits for the Job for a ZAProxy resource to be completed.
func waitForJobCompletion(ctx context.Context, c client.Client, job *kbatch.Job) error {

	for {
		time.Sleep(time.Second)

		if ctx.Err() != nil {
			return fmt.Errorf("timed out waiting for job completion with job name %s in namespace %s: %w", job.Name, job.Namespace, ctx.Err())
		}

		err := c.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, job)
		if err != nil {
			continue
		}

		if job.Status.CompletionTime != nil {
			return nil
		}
	}
}

// deleteJob deletes the Job for a ZAProxy resource.
func deleteJob(ctx context.Context, c client.Client, job *kbatch.Job) error {

	if err := c.Delete(ctx, job); err != nil {
		return fmt.Errorf("unable to delete old Job for ZAProxy (Job.Namespace: %s, Job.Name: %s): %w", job.Namespace, job.Name, err)
	}

	if err := waitForJobDeletion(ctx, c, job); err != nil {
		return fmt.Errorf("error waiting for job deletion (Job.Namespace: %s, Job.Name: %s): %w", job.Namespace, job.Name, err)
	}

	return nil
}

// getJobPodIP returns the IP address of the Pod for a Job.
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

// getJobContainerPort returns the port of the first container in a Job.
func getJobContainerPort(job *kbatch.Job) (int32, error) {
	if len(job.Spec.Template.Spec.Containers) > 0 {
		if len(job.Spec.Template.Spec.Containers[0].Ports) > 0 {
			return job.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort, nil
		}
		return 0, fmt.Errorf("no ports found for the first container in job %s in namespace %s", job.Name, job.Namespace)
	}
	return 0, fmt.Errorf("no containers found in the job %s in namespace %s", job.Name, job.Namespace)
}

// sendHTTPRequest sends an HTTP request to a URL.
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
