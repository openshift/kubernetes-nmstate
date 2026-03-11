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

package cluster

import (
	"context"
	"crypto/tls"
	"fmt"

	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cluster")

// IsOpenShift returns always true since this is the openshift fork
func IsOpenShift(kclient client.Client) (bool, error) {
	return true, nil
}

// FetchOpenShiftTLSOpts detects OpenShift and fetches the TLS profile for
// secure serving. Returns nil on non-OpenShift clusters.
func FetchOpenShiftTLSOpts(scheme *runtime.Scheme) (func(*tls.Config), error) {
	cfg := ctrl.GetConfigOrDie()
	kclient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed creating client for TLS profile detection: %w", err)
	}

	isOCP, err := IsOpenShift(kclient)
	if err != nil {
		log.Info("Warning: could not determine if running on OpenShift, assuming not")
		return nil, nil
	}
	if !isOCP {
		return nil, nil
	}

	tlsProfileSpec, err := tlspkg.FetchAPIServerTLSProfile(context.Background(), kclient)
	if err != nil {
		return nil, fmt.Errorf("unable to get TLS profile from API server: %w", err)
	}

	tlsOpts, unsupportedCiphers := tlspkg.NewTLSConfigFromProfile(tlsProfileSpec)
	if len(unsupportedCiphers) > 0 {
		log.Info("TLS configuration contains unsupported ciphers that will be ignored",
			"unsupportedCiphers", unsupportedCiphers)
	}

	return tlsOpts, nil
}