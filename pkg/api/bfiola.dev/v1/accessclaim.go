// +k8s:deepcopy-gen=package

package v1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AccessClaim specifies desired connectivity to cluster resources
type AccessClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccessClaimSpec   `json:"spec"`
	Status AccessClaimStatus `json:"status,omitempty"`
}

// AccessClaimSpec defines the desired state of AccessClaim
type AccessClaimSpec struct {
	Dns              string                    `json:"dns"`
	IngressTemplate  *networkingv1.IngressSpec `json:"ingressTemplate,omitempty"`
	PasswordRef      SecretKeyRef              `json:"passwordRef"`
	ServiceTemplates []corev1.ServiceSpec      `json:"serviceTemplates,omitempty"`
	Ttl              string                    `json:"ttl"`
}

// AccessClaimStatus defines the current state of AccessClaim
type AccessClaimStatus struct {
	CurrentSpec *AccessClaimSpec `json:"currentSpec,omitempty"`
}

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AccessClaimList contains a list of AccessClaim
type AccessClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []AccessClaim `json:"items"`
}
