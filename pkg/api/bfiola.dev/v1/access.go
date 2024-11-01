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

// Access specifies the current connectivity to cluster resources
type Access struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccessSpec   `json:"spec"`
	Status AccessStatus `json:"status,omitempty"`
}

type Member struct {
	Cidr    string `json:"cidr"`
	Expires string `json:"expires"`
}

// AccessSpec defines the desired state of Access
type AccessSpec struct {
	Dns              string                    `json:"dns"`
	IngressTemplate  *networkingv1.IngressSpec `json:"ingressTemplate,omitempty"`
	Members          []Member                  `json:"members"`
	PasswordRef      SecretKeyRef              `json:"passwordRef"`
	ServiceTemplates []corev1.ServiceSpec      `json:"serviceTemplates,omitempty"`
	Ttl              string                    `json:"ttl"`
}

// AccessStatus defines the current state of Access
type AccessStatus struct {
	CurrentSpec *AccessSpec `json:"currentSpec,omitempty"`
}

// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AccessList contains a list of Access
type AccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Access `json:"items"`
}
