/*
Copyright The Kubernetes NMState Authors.


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

package v1beta1

import (
	"github.com/nmstate/kubernetes-nmstate/api/shared"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NMStateSpec defines the desired state of NMState
type NMStateSpec struct {
	// Affinity is an optional affinity selector that will be added to handler DaemonSet manifest.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// InfraAffinity is an optional affinity selector that will be added to webhook, metrics & console-plugin Deployment manifests.
	// +optional
	InfraAffinity *corev1.Affinity `json:"infraAffinity,omitempty"`
	// NodeSelector is an optional selector that will be added to handler DaemonSet manifest
	// for both workers and control-plane (https://github.com/nmstate/kubernetes-nmstate/blob/main/deploy/handler/operator.yaml).
	// If NodeSelector is specified, the handler will run only on nodes that have each of the indicated key-value pairs
	// as labels applied to the node.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Tolerations is an optional list of tolerations to be added to handler DaemonSet manifest
	// If Tolerations is specified, the handler daemonset will be also scheduled on nodes with corresponding taints
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// InfraNodeSelector is an optional selector that will be added to webhook, metrics & console-plugin Deployment manifests
	// If InfraNodeSelector is specified, the webhook, metrics and the console plugin will run only on nodes that have each
	// of the indicated key-value pairs as labels applied to the node.
	// +optional
	InfraNodeSelector map[string]string `json:"infraNodeSelector,omitempty"`
	// InfraTolerations is an optional list of tolerations to be added to webhook, metrics & console-plugin Deployment manifests
	// If InfraTolerations is specified, the webhook, metrics and the console plugin will be able to be scheduled on nodes with
	// corresponding taints
	// +optional
	InfraTolerations []corev1.Toleration `json:"infraTolerations,omitempty"`
	// SelfSignConfiguration defines self signed certificate configuration
	SelfSignConfiguration *SelfSignConfiguration `json:"selfSignConfiguration,omitempty"`
	// ProbeConfiguration is an optional configuration of NMstate probes testing various functionalities.
	// If ProbeConfiguration is specified, the handler will use the config defined here instead of its default values.
	// +kubebuilder:default:={}
	// +optional
	ProbeConfiguration NMStateProbeConfiguration `json:"probeConfiguration,omitempty"`
}

type SelfSignConfiguration struct {
	// CARotateInterval defines duration for CA expiration
	CARotateInterval string `json:"caRotateInterval,omitempty"`
	// CAOverlapInterval defines the duration where expired CA certificate
	// can overlap with new one, in order to allow fluent CA rotation transitioning
	CAOverlapInterval string `json:"caOverlapInterval,omitempty"`
	// CertRotateInterval defines duration for of service certificate expiration
	CertRotateInterval string `json:"certRotateInterval,omitempty"`
	// CertOverlapInterval defines the duration where expired service certificate
	// can overlap with new one, in order to allow fluent service rotation transitioning
	CertOverlapInterval string `json:"certOverlapInterval,omitempty"`
}

type NMStateProbeConfiguration struct {
	// +kubebuilder:default={"host": "root-servers.net"}
	DNS NMStateDNSProbeConfiguration `json:"dns,omitempty"`
}

type NMStateDNSProbeConfiguration struct {
	// +kubebuilder:default:="root-servers.net"
	// +required
	Host string `json:"host,omitempty"`
}

// NMStateStatus defines the observed state of NMState
type NMStateStatus struct {
	Conditions shared.ConditionList `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=nmstates,scope=Cluster
// +kubebuilder:subresource=status
// +kubebuilder:deprecatedversion

// NMState is the Schema for the nmstates API
type NMState struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// We are adding a default value for the Spec field because we want it to get automatically
	// populated even if user does not specify it at all. By default, the k8s apiserver populates
	// defaults only if you define "spec: {}" in your CR, otherwise it ignores the spec tree.
	// Ref.: https://ahmet.im/blog/crd-generation-pitfalls/#defaulting-on-nested-structs

	// +kubebuilder:default:={}
	Spec   NMStateSpec   `json:"spec,omitempty"`
	Status NMStateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NMStateList contains a list of NMState
type NMStateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NMState `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NMState{}, &NMStateList{})
}
