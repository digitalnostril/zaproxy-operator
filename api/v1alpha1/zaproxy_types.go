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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ZAProxySpec defines the desired state of ZAProxy
type ZAProxySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	// +kubebuilder:validation:ExclusiveMaximum=false

	// Size defines the number of Memcached instances
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Size int32 `json:"size,omitempty"`

	// Port defines the port that will be used to init the container with the image
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	ContainerPort int32 `json:"containerPort,omitempty"`

	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Automation Automation `json:"automation,omitempty"`

	// +operator-sdk:csv:customresourcedefinitions:type=spec
	StorageClassName string `json:"storageClassName,omitempty"`
}

type Automation struct {
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Plan runtime.RawExtension `json:"plan,omitempty"`
}

// ZAProxyStatus defines the observed state of ZAProxy
type ZAProxyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +operator-sdk:csv:customresourcedefinitions:resources={{Deployment,v1,zaproxy-deployment}}

// ZAProxy is the Schema for the zaproxies API
type ZAProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZAProxySpec   `json:"spec,omitempty"`
	Status ZAProxyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ZAProxyList contains a list of ZAProxy
// +kubebuilder:subresource:status
type ZAProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZAProxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZAProxy{}, &ZAProxyList{})
}
