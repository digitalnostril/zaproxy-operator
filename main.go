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

package main

import (
	"flag"
	"net/http"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
	"github.com/digitalnostril/zaproxy-operator/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(zaproxyorgv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "2363f4c8.zaproxy.org",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Try registering with the existing webhook server.
	mgr.GetWebhookServer().Register("/zap/start", &MyHandler{Client: mgr.GetClient()})

	mgr.GetWebhookServer().Register("/zap/enddelay", &EndDelay{Client: mgr.GetClient()})

	if err = (&controllers.ZAProxyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("zaproxy-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ZAProxy")
		os.Exit(1)
	}
	if err = (&zaproxyorgv1alpha1.ZAProxy{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ZAProxy")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type MyHandler struct {
	Client client.Client
}

func (h *MyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	name := r.URL.Query().Get("name")
	namespace := r.URL.Query().Get("namespace")

	if name == "" || namespace == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Both 'name' and 'namespace' query parameters must be provided"))
		return
	}

	_, err := controllers.CreateJob(name, namespace, h.Client)
	if err != nil {

		setupLog.Error(err, "Failed to create job")
		w.Write([]byte("Failed to create job"))
	}
	w.Write([]byte("Hello, world!"))
}

type EndDelay struct {
	Client client.Client
}

func (h *EndDelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	name := r.URL.Query().Get("name")
	namespace := r.URL.Query().Get("namespace")

	if name == "" || namespace == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Both 'name' and 'namespace' query parameters must be provided"))
		return
	}

	_, err := controllers.EndDelayZAPJob(name, namespace, h.Client)
	if err != nil {

		setupLog.Error(err, "Failed to end zap job")
		w.Write([]byte("Failed to end zap job"))
	}
	w.Write([]byte("Hello, world!"))
}
