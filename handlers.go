package main

import (
	"net/http"

	"github.com/digitalnostril/zaproxy-operator/controllers"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StartJobHandler struct {
	Client client.Client
}

func (h *StartJobHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	const (
		ErrFailedToCreateJob      = "Failed to create job"
		MsgJobCreatedSuccessfully = "Job created successfully"
	)

	values, err := retrieveAndValidateQueryParams(r, "name", "namespace")
	if err != nil {
		setupLog.Error(err, err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	namespacedName := client.ObjectKey{
		Namespace: values["namespace"],
		Name:      values["name"],
	}

	if _, err := controllers.CreateJob(r.Context(), h.Client, namespacedName); err != nil {
		setupLog.Error(err, ErrFailedToCreateJob, namespacedNameToKeyValueSlice(namespacedName)...)
		http.Error(w, ErrFailedToCreateJob, http.StatusInternalServerError)
		return
	}

	setupLog.Info(MsgJobCreatedSuccessfully, namespacedNameToKeyValueSlice(namespacedName)...)
	respondOK(w, MsgJobCreatedSuccessfully)
}

type EndDelayZAPJobHandler struct {
	Client client.Client
}

func (h *EndDelayZAPJobHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	const (
		ErrFailedToEndJob       = "Failed to end ZAP delay job"
		MsgJobEndedSuccessfully = "ZAP delay job ended successfully"
	)

	values, err := retrieveAndValidateQueryParams(r, "name", "namespace")
	if err != nil {
		setupLog.Error(err, err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	namespacedName := client.ObjectKey{
		Namespace: values["namespace"],
		Name:      values["name"],
	}

	if _, err := controllers.EndDelayZAPJob(r.Context(), h.Client, namespacedName); err != nil {
		setupLog.Error(err, ErrFailedToEndJob, namespacedNameToKeyValueSlice(namespacedName)...)
		http.Error(w, ErrFailedToEndJob, http.StatusInternalServerError)
		return
	}

	setupLog.Info(MsgJobEndedSuccessfully, namespacedNameToKeyValueSlice(namespacedName)...)
	respondOK(w, MsgJobEndedSuccessfully)
}
