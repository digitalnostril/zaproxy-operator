package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func retrieveAndValidateQueryParams(r *http.Request, params ...string) (map[string]string, error) {
	values := make(map[string]string)

	for _, param := range params {
		value := r.URL.Query().Get(param)
		if value == "" {
			return nil, fmt.Errorf("'%s' query parameter must be provided", param)
		}
		values[param] = value
	}

	return values, nil
}

func respondOK(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": message,
	}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func namespacedNameToKeyValueSlice(namespacedName client.ObjectKey) []interface{} {
	return []interface{}{
		"Name", namespacedName.Name,
		"Namespace", namespacedName.Namespace,
	}
}
