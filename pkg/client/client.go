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

package client

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/nmstate/kubernetes-nmstate/api/names"
	"github.com/nmstate/kubernetes-nmstate/api/shared"
	nmstatev1beta1 "github.com/nmstate/kubernetes-nmstate/api/v1beta1"
	"github.com/nmstate/kubernetes-nmstate/pkg/bridge"
	"github.com/nmstate/kubernetes-nmstate/pkg/nmstatectl"
	"github.com/nmstate/kubernetes-nmstate/pkg/probe"
)

var (
	log = logf.Log.WithName("client")
)

const (
	defaultGwProbeTimeout = 120 * time.Second
	apiServerProbeTimeout = 120 * time.Second
	// DesiredStateConfigurationTimeout doubles the default gw ping probe and API server
	// connectivity check timeout to ensure the Checkpoint is alive before rolling it back
	// https://nmstate.github.io/cli_guide#manual-transaction-control
	DesiredStateConfigurationTimeout = (defaultGwProbeTimeout + apiServerProbeTimeout) * 2
)

type DependencyVersions struct {
	HandlerNetworkManagerVersion string
	HandlerNmstateVersion        string
	HostNmstateVersion           string
}

func InitializeNodeNetworkState(cli client.Client, node *corev1.Node) (*nmstatev1beta1.NodeNetworkState, error) {
	ownerRefList := []metav1.OwnerReference{{Name: node.ObjectMeta.Name, Kind: "Node", APIVersion: "v1", UID: node.UID}}

	nodeNetworkState := nmstatev1beta1.NodeNetworkState{
		// Create NodeNetworkState for this node
		ObjectMeta: metav1.ObjectMeta{
			Name:            node.ObjectMeta.Name,
			OwnerReferences: ownerRefList,
			Labels:          names.IncludeRelationshipLabels(nil),
		},
	}

	err := cli.Create(context.TODO(), &nodeNetworkState)
	if err != nil {
		return nil, fmt.Errorf("error creating NodeNetworkState: %v, %+v", err, nodeNetworkState)
	}

	return &nodeNetworkState, nil
}

func CreateOrUpdateNodeNetworkState(
	cli client.Client,
	node *corev1.Node,
	observedState shared.State,
	nns *nmstatev1beta1.NodeNetworkState,
	versions *DependencyVersions,
) error {
	if nns == nil {
		var err error
		nns, err = InitializeNodeNetworkState(cli, node)
		if err != nil {
			return err
		}
	}
	return UpdateCurrentState(cli, nns, observedState, versions)
}

func UpdateCurrentState(
	cli client.Client,
	nodeNetworkState *nmstatev1beta1.NodeNetworkState,
	observedState shared.State,
	versions *DependencyVersions,
) error {
	if observedState.String() == nodeNetworkState.Status.CurrentState.String() {
		log.Info("Skipping NodeNetworkState update, node network configuration not changed")
		return nil
	}

	nodeNetworkState.Status.HandlerNetworkManagerVersion = versions.HandlerNetworkManagerVersion
	nodeNetworkState.Status.HandlerNmstateVersion = versions.HandlerNmstateVersion
	nodeNetworkState.Status.HostNetworkManagerVersion = versions.HostNmstateVersion

	nodeNetworkState.Status.CurrentState = observedState
	nodeNetworkState.Status.LastSuccessfulUpdateTime = metav1.Time{Time: time.Now()}

	err := cli.Status().Update(context.Background(), nodeNetworkState)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errors.Wrap(err, "Request object not found, could have been deleted after reconcile request")
		} else {
			return errors.Wrap(err, "Error updating nodeNetworkState")
		}
	}

	return nil
}

func ExecuteCommand(command string, arguments ...string) (string, error) {
	cmd := exec.Command(command, arguments...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to execute %s: '%s', '%s', '%s'", command, err.Error(), stdout.String(), stderr.String())
	}

	return string(bytes.Trim(stdout.Bytes(), "\n")), nil
}

func rollback(cli client.Client, probes []probe.Probe, cause error) error {
	message := fmt.Sprintf("rolling back desired state configuration: %s", cause)
	err := nmstatectl.Rollback()
	if err != nil {
		return errors.Wrap(err, message)
	}

	// wait for system to settle after rollback
	probesErr := probe.Run(cli, probes)
	if probesErr != nil {
		return errors.Wrap(errors.Wrap(probesErr, "failed running probes after rollback"), message)
	}
	return errors.New(message)
}

func ApplyDesiredState(cli client.Client, desiredState shared.State) (string, error) {
	if string(desiredState.Raw) == "" {
		return "Ignoring empty desired state", nil
	}

	out, err := bridge.EnableVlanFiltering(desiredState)
	if err != nil {
		return out, fmt.Errorf("failed to enable vlan filtering via nmcli: %s", err.Error())
	}

	// Before apply we get the probes that are working fine, they should be
	// working fine after apply
	probes := probe.Select(cli)

	// Rollback before Apply to remove pending checkpoints (for example handler pod restarted
	// before Commit)
	nmstatectl.Rollback()

	setOutput, err := nmstatectl.Set(desiredState, DesiredStateConfigurationTimeout)
	if err != nil {
		return setOutput, err
	}

	err = probe.Run(cli, probes)
	if err != nil {
		return "", rollback(cli, probes, errors.Wrap(err, "failed runnig probes after network changes"))
	}

	commitOutput, err := nmstatectl.Commit()
	if err != nil {
		// We cannot rollback if commit fails, just return the error
		return commitOutput, err
	}

	commandOutput := fmt.Sprintf("setOutput: %s \n", setOutput)
	return commandOutput, nil
}
