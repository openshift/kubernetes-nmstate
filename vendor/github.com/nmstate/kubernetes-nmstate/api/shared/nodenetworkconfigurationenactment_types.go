package shared

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// NodeNetworkConfigurationEnactmentStatus defines the observed state of NodeNetworkConfigurationEnactment
type NodeNetworkConfigurationEnactmentStatus struct {
	// +kubebuilder:validation:XPreserveUnknownFields
	// The desired state rendered for the enactment's node using
	// the policy desiredState as template
	DesiredState State `json:"desiredState,omitempty"`

	DesiredStateMetaInfo NodeNetworkConfigurationEnactmentMetaInfo `json:"desiredStateMetaInfo,omitempty"`

	// A cache containing the resolved captures after processing the capture at NNCP
	CapturedStates map[string]NodeNetworkConfigurationEnactmentCapturedState `json:"capturedStates,omitempty"`

	// The generation from policy needed to check if an enactment
	// condition status belongs to the same policy version
	PolicyGeneration int64 `json:"policyGeneration,omitempty"`

	Conditions ConditionList `json:"conditions,omitempty"`
}

type NodeNetworkConfigurationEnactmentCapturedState struct {
	// +kubebuilder:validation:XPreserveUnknownFields
	State    State                                     `json:"state,omitempty"`
	MetaInfo NodeNetworkConfigurationEnactmentMetaInfo `json:"metaInfo,omitempty"`
}

type NodeNetworkConfigurationEnactmentMetaInfo struct {
	Version   string      `json:"version,omitempty"`
	TimeStamp metav1.Time `json:"time,omitempty"`
}

const (
	EnactmentPolicyLabel                                                = "nmstate.io/policy"
	EnactmentNodeLabel                                                  = "nmstate.io/node"
	NodeNetworkConfigurationEnactmentConditionAvailable   ConditionType = "Available"
	NodeNetworkConfigurationEnactmentConditionFailing     ConditionType = "Failing"
	NodeNetworkConfigurationEnactmentConditionPending     ConditionType = "Pending"
	NodeNetworkConfigurationEnactmentConditionProgressing ConditionType = "Progressing"
	NodeNetworkConfigurationEnactmentConditionAborted     ConditionType = "Aborted"
)

var NodeNetworkConfigurationEnactmentConditionTypes = [...]ConditionType{
	NodeNetworkConfigurationEnactmentConditionAvailable,
	NodeNetworkConfigurationEnactmentConditionFailing,
	NodeNetworkConfigurationEnactmentConditionProgressing,
	NodeNetworkConfigurationEnactmentConditionPending,
	NodeNetworkConfigurationEnactmentConditionAborted,
}

const (
	NodeNetworkConfigurationEnactmentConditionFailedToConfigure          ConditionReason = "FailedToConfigure"
	NodeNetworkConfigurationEnactmentConditionSuccessfullyConfigured     ConditionReason = "SuccessfullyConfigured"
	NodeNetworkConfigurationEnactmentConditionMaxUnavailableLimitReached ConditionReason = "MaxUnavailableLimitReached"
	NodeNetworkConfigurationEnactmentConditionConfigurationProgressing   ConditionReason = "ConfigurationProgressing"
	NodeNetworkConfigurationEnactmentConditionConfigurationAborted       ConditionReason = "ConfigurationAborted"
)

func EnactmentKey(node string, policy string) types.NamespacedName {
	return types.NamespacedName{Name: fmt.Sprintf("%s.%s", node, policy)}
}
