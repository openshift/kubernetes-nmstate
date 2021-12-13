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

package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// +kubebuilder:scaffold:imports

	"github.com/gofrs/flock"
	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/qinqon/kube-admission-webhook/pkg/certificate"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/nmstate/kubernetes-nmstate/api/names"
	nmstateapi "github.com/nmstate/kubernetes-nmstate/api/shared"
	nmstatev1 "github.com/nmstate/kubernetes-nmstate/api/v1"
	nmstatev1alpha1 "github.com/nmstate/kubernetes-nmstate/api/v1alpha1"
	nmstatev1beta1 "github.com/nmstate/kubernetes-nmstate/api/v1beta1"
	controllers "github.com/nmstate/kubernetes-nmstate/controllers/handler"
	"github.com/nmstate/kubernetes-nmstate/pkg/environment"
	"github.com/nmstate/kubernetes-nmstate/pkg/file"
	"github.com/nmstate/kubernetes-nmstate/pkg/nmstatectl"
	"github.com/nmstate/kubernetes-nmstate/pkg/webhook"
)

type ProfilerConfig struct {
	EnableProfiler bool   `envconfig:"ENABLE_PROFILER"`
	ProfilerPort   string `envconfig:"PROFILER_PORT" default:"6060"`
}

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(nmstatev1.AddToScheme(scheme))
	utilruntime.Must(nmstatev1beta1.AddToScheme(scheme))
	utilruntime.Must(nmstatev1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	opt := zap.Options{}
	opt.BindFlags(flag.CommandLine)
	var logType string
	pflag.StringVar(&logType, "v", "production", "Log type (debug/production).")
	pflag.CommandLine.MarkDeprecated("v", "please use the --zap-devel flag for debug logging instead")
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	if logType == "debug" {
		// workaround until --v flag got removed
		flag.CommandLine.Set("zap-devel", "true")
	}

	setupLogger(opt)

	// Lock only for handler, we can run old and new version of
	// webhook without problems, policy status will be updated
	// by multiple instances.
	if environment.IsHandler() {
		handlerLock, err := lockHandler()
		if err != nil {
			setupLog.Error(err, "Failed to run lockHandler")
			os.Exit(1)
		}
		defer handlerLock.Unlock()
		setupLog.Info("Successfully took nmstate exclusive lock")
	}

	ctrlOptions := ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0", // disable metrics
	}

	if environment.IsHandler() {
		// Handler runs as a daemonset and we want that each handler pod will cache/reconcile only resources belongs the node it runs on.
		nodeName := environment.NodeName()
		metadataNameMatchingNodeNameSelector := fields.Set{"metadata.name": nodeName}.AsSelector()
		nodelabelMatchingNodeNameSelector := labels.Set{nmstateapi.EnactmentNodeLabel: nodeName}.AsSelector()
		ctrlOptions.NewCache = cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&corev1.Node{}: {
					Field: metadataNameMatchingNodeNameSelector,
				},
				&nmstatev1beta1.NodeNetworkState{}: {
					Field: metadataNameMatchingNodeNameSelector,
				},
				&nmstatev1beta1.NodeNetworkConfigurationEnactment{}: {
					Label: nodelabelMatchingNodeNameSelector,
				},
			},
		})
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrlOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if environment.IsCertManager() {
		certManagerOpts := certificate.Options{
			Namespace:   os.Getenv("POD_NAMESPACE"),
			WebhookName: "nmstate",
			WebhookType: certificate.MutatingWebhook,
			ExtraLabels: names.IncludeRelationshipLabels(nil),
		}

		certManagerOpts.CARotateInterval, err = environment.LookupAsDuration("CA_ROTATE_INTERVAL")
		if err != nil {
			setupLog.Error(err, "Failed retrieving ca rotate interval")
			os.Exit(1)
		}

		certManagerOpts.CAOverlapInterval, err = environment.LookupAsDuration("CA_OVERLAP_INTERVAL")
		if err != nil {
			setupLog.Error(err, "Failed retrieving ca overlap interval")
			os.Exit(1)
		}

		certManagerOpts.CertRotateInterval, err = environment.LookupAsDuration("CERT_ROTATE_INTERVAL")
		if err != nil {
			setupLog.Error(err, "Failed retrieving cert rotate interval")
			os.Exit(1)
		}

		certManagerOpts.CertOverlapInterval, err = environment.LookupAsDuration("CERT_OVERLAP_INTERVAL")
		if err != nil {
			setupLog.Error(err, "Failed retrieving cert overlap interval")
			os.Exit(1)
		}

		certManager, err := certificate.NewManager(mgr.GetClient(), certManagerOpts)
		if err != nil {
			setupLog.Error(err, "unable to create cert-manager", "controller", "cert-manager")
			os.Exit(1)
		}

		err = certManager.Add(mgr)
		if err != nil {
			setupLog.Error(err, "unable to add cert-manager to controller-runtime manager", "controller", "cert-manager")
			os.Exit(1)
		}
		// Runs only webhook controllers if it's specified
	} else if environment.IsWebhook() {
		if err := webhook.AddToManager(mgr); err != nil {
			setupLog.Error(err, "Cannot initialize webhook")
			os.Exit(1)
		}
	} else if environment.IsHandler() {
		if err = (&controllers.NodeReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("Node"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create Node controller", "controller", "NMState")
			os.Exit(1)
		}

		apiClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme(), Mapper: mgr.GetRESTMapper()})
		if err != nil {
			setupLog.Error(err, "failed creating non cached client")
			os.Exit(1)
		}

		if err = (&controllers.NodeNetworkConfigurationPolicyReconciler{
			Client:    mgr.GetClient(),
			APIClient: apiClient,
			Log:       ctrl.Log.WithName("controllers").WithName("NodeNetworkConfigurationPolicy"),
			Scheme:    mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create NodeNetworkConfigurationPolicy controller", "controller", "NMState")
			os.Exit(1)
		}

		if err = (&controllers.NodeNetworkConfigurationEnactmentReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("NodeNetworkConfigurationEnactment"),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create NodeNetworkConfigurationEnactment controller", "controller", "NMState")
			os.Exit(1)
		}

		// Check that nmstatectl is working
		_, err = nmstatectl.Show()
		if err != nil {
			setupLog.Error(err, "failed checking nmstatectl health")
			os.Exit(1)
		}

		// Handler runs with host networking so opening ports is problematic
		// they will collide with node ports so to ensure that we reach this
		// point (we have the handler lock and nmstatectl show is working) a
		// file is touched and the file is checked at readinessProbe field.
		healthyFile := "/tmp/healthy"
		setupLog.Info("Marking handler as healthy touching healthy file", "healthyFile", healthyFile)
		err = file.Touch(healthyFile)
		if err != nil {
			setupLog.Error(err, "failed marking handler as healthy")
			os.Exit(1)
		}
	}

	setProfiler()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupLogger(opts zap.Options) {
	opts.EncoderConfigOptions = append(opts.EncoderConfigOptions, func(ec *zapcore.EncoderConfig) {
		ec.EncodeTime = zapcore.ISO8601TimeEncoder
	})

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)
}

// Start profiler on given port if ENABLE_PROFILER is True
func setProfiler() {
	cfg := ProfilerConfig{}
	envconfig.Process("", &cfg)
	if cfg.EnableProfiler {
		setupLog.Info("Starting profiler")
		go func() {
			profilerAddress := fmt.Sprintf("0.0.0.0:%s", cfg.ProfilerPort)
			setupLog.Info(fmt.Sprintf("Starting Profiler Server! \t Go to http://%s/debug/pprof/\n", profilerAddress))
			err := http.ListenAndServe(profilerAddress, nil)
			if err != nil {
				setupLog.Info("Failed to start the server! Error: %v", err)
			}
		}()
	}
}

func lockHandler() (*flock.Flock, error) {
	lockFilePath, ok := os.LookupEnv("NMSTATE_INSTANCE_NODE_LOCK_FILE")
	if !ok {
		return nil, errors.New("Failed to find NMSTATE_INSTANCE_NODE_LOCK_FILE ENV var")
	}
	setupLog.Info(fmt.Sprintf("Try to take exclusive lock on file: %s", lockFilePath))
	handlerLock := flock.New(lockFilePath)
	err := wait.PollImmediateInfinite(5*time.Second, func() (done bool, err error) {
		locked, err := handlerLock.TryLock()
		if err != nil {
			setupLog.Error(err, "retrying to lock handler")
			return false, nil // Don't return the error here, it will not re-poll if we do
		}
		return locked, nil
	})
	return handlerLock, err
}
