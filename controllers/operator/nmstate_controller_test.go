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

package controllers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/nmstate/kubernetes-nmstate/api/names"
	nmstatev1 "github.com/nmstate/kubernetes-nmstate/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
)

var _ = Describe("NMState controller reconcile", func() {
	var (
		cl                         client.Client
		reconciler                 NMStateReconciler
		existingNMStateName        = "nmstate"
		defaultHandlerNodeSelector = map[string]string{"kubernetes.io/os": "linux", "kubernetes.io/arch": goruntime.GOARCH}
		customHandlerNodeSelector  = map[string]string{"selector_1": "value_1", "selector_2": "value_2"}
		handlerTolerations         = []corev1.Toleration{
			{
				Effect:   "NoSchedule",
				Key:      "node.kubernetes.io/special-toleration",
				Operator: "Exists",
			},
		}
		infraNodeSelector = map[string]string{"webhookselector_1": "value_1", "webhookselector_2": "value_2"}
		infraTolerations  = []corev1.Toleration{
			{
				Effect:   "NoSchedule",
				Key:      "node.kubernetes.io/special-webhook-toleration",
				Operator: "Exists",
			},
		}
		nmstate = nmstatev1.NMState{
			ObjectMeta: metav1.ObjectMeta{
				Name: existingNMStateName,
				UID:  "12345",
			},
		}
		handlerPrefix       = "handler"
		handlerNamespace    = "nmstate"
		operatorNamespace   = "nmstate"
		handlerKey          = types.NamespacedName{Namespace: handlerNamespace, Name: handlerPrefix + "-nmstate-handler"}
		webhookKey          = types.NamespacedName{Namespace: handlerNamespace, Name: handlerPrefix + "-nmstate-webhook"}
		handlerImage        = "quay.io/some_image"
		monitoringNamespace = "monitoring"
		kubeRBACProxyImage  = "quay.io/some_kube_rbac_proxy_image"
		imagePullPolicy     = "Always"
		manifestsDir        = ""
	)
	BeforeEach(func() {
		var err error
		manifestsDir, err = os.MkdirTemp("/tmp", "knmstate-test-manifests")
		Expect(err).ToNot(HaveOccurred())
		err = copyManifests(manifestsDir)
		Expect(err).ToNot(HaveOccurred())

		s := scheme.Scheme
		s.AddKnownTypes(nmstatev1.GroupVersion,
			&nmstatev1.NMState{},
			&nmstatev1.NMStateList{},
		)
		objs := []runtime.Object{&nmstate}
		// Create a fake client to mock API calls.
		cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
		names.ManifestDir = manifestsDir
		reconciler.Client = cl
		reconciler.APIClient = cl
		reconciler.Scheme = s
		reconciler.Log = ctrl.Log.WithName("controllers").WithName("NMState")
		os.Setenv("HANDLER_NAMESPACE", handlerNamespace)
		os.Setenv("OPERATOR_NAMESPACE", operatorNamespace)
		os.Setenv("HANDLER_IMAGE", handlerImage)
		os.Setenv("HANDLER_IMAGE_PULL_POLICY", imagePullPolicy)
		os.Setenv("HANDLER_PREFIX", handlerPrefix)
		os.Setenv("MONITORING_NAMESPACE", monitoringNamespace)
		os.Setenv("KUBE_RBAC_PROXY_IMAGE", kubeRBACProxyImage)
	})
	AfterEach(func() {
		err := os.RemoveAll(manifestsDir)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("when additional CR is created", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			request.Name = "nmstate-two"
		})
		It("should return empty result", func() {
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("and should delete the second one", func() {
			_, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			nmstateList := &nmstatev1.NMStateList{}
			err = cl.List(context.TODO(), nmstateList, &client.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nmstateList.Items)).To(Equal(1))
		})
	})
	Context("when an nmstate is found", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			request.Name = existingNMStateName
		})
		It("should return a Result", func() {
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
	})
	Context("when one of manifest directory is empty", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			request.Name = existingNMStateName
			crdsPath := filepath.Join(manifestsDir, "kubernetes-nmstate", "crds")
			dir, err := os.ReadDir(crdsPath)
			Expect(err).ToNot(HaveOccurred())
			for _, d := range dir {
				os.RemoveAll(filepath.Join(crdsPath, d.Name()))
			}
		})
		It("should return error", func() {
			_, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).To(HaveOccurred())
		})
	})
	Context("when operator spec has a NodeSelector", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			// set NodeSelector field in operator Spec
			nmstate.Spec.NodeSelector = customHandlerNodeSelector
			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should not add default NodeSelector to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range defaultHandlerNodeSelector {
				Expect(ds.Spec.Template.Spec.NodeSelector).ToNot(HaveKeyWithValue(k, v))
			}
		})
		It("should add NodeSelector to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range customHandlerNodeSelector {
				Expect(ds.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(k, v))
			}
		})
		It("should NOT add NodeSelector to webhook deployment", func() {
			deployment := &appsv1.Deployment{}
			err := cl.Get(context.TODO(), webhookKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range customHandlerNodeSelector {
				Expect(deployment.Spec.Template.Spec.NodeSelector).ToNot(HaveKeyWithValue(k, v))
			}
		})
	})
	Context("when operator spec has Tolerations", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			// set Tolerations field in operator Spec
			nmstate.Spec.Tolerations = handlerTolerations
			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should add Tolerations to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			Expect(allTolerationsPresent(handlerTolerations, ds.Spec.Template.Spec.Tolerations)).To(BeTrue())
		})
		It("should NOT add Tolerations to webhook deployment", func() {
			deployment := &appsv1.Deployment{}
			err := cl.Get(context.TODO(), webhookKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			Expect(anyTolerationsPresent(handlerTolerations, deployment.Spec.Template.Spec.Tolerations)).To(BeFalse())
		})
	})
	Context("when operator spec has a InfraNodeSelector", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			// set InfraNodeSelector field in operator Spec
			nmstate.Spec.InfraNodeSelector = infraNodeSelector
			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should add InfraNodeSelector to webhook deployment", func() {
			deployment := &appsv1.Deployment{}
			err := cl.Get(context.TODO(), webhookKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range infraNodeSelector {
				Expect(deployment.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(k, v))
			}
		})
		It("should add InfraNodeSelector to metrics deployment", func() {
			deployment := &appsv1.Deployment{}
			metricsKey := types.NamespacedName{Namespace: handlerNamespace, Name: handlerPrefix + "-nmstate-metrics"}
			err := cl.Get(context.TODO(), metricsKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range infraNodeSelector {
				Expect(deployment.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(k, v))
			}
		})
		It("should NOT add InfraNodeSelector to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range infraNodeSelector {
				Expect(ds.Spec.Template.Spec.NodeSelector).ToNot(HaveKeyWithValue(k, v))
			}
		})
	})
	Context("when operator spec has InfraTolerations", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			// set Tolerations field in operator Spec
			nmstate.Spec.InfraTolerations = infraTolerations
			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should add InfraTolerations to webhook deployment", func() {
			deployment := &appsv1.Deployment{}
			err := cl.Get(context.TODO(), webhookKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			Expect(allTolerationsPresent(infraTolerations, deployment.Spec.Template.Spec.Tolerations)).To(BeTrue())
		})
		It("should add InfraTolerations to metrics deployment", func() {
			deployment := &appsv1.Deployment{}
			metricsKey := types.NamespacedName{Namespace: handlerNamespace, Name: handlerPrefix + "-nmstate-metrics"}
			err := cl.Get(context.TODO(), metricsKey, deployment)
			Expect(err).ToNot(HaveOccurred())
			Expect(allTolerationsPresent(infraTolerations, deployment.Spec.Template.Spec.Tolerations)).To(BeTrue())
		})

		It("should NOT add InfraTolerations to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			Expect(anyTolerationsPresent(infraTolerations, ds.Spec.Template.Spec.Tolerations)).To(BeFalse())
		})
	})

	Context("when operator spec has custom DNS probe configured", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			// set DNS probe config field in operator Spec
			nmstate.Spec.ProbeConfiguration = nmstatev1.NMStateProbeConfiguration{
				DNS: nmstatev1.NMStateDNSProbeConfiguration{
					Host: "google.com",
				},
			}

			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should add DNS probe host to handler daemonset", func() {
			ds := &appsv1.DaemonSet{}
			err := cl.Get(context.TODO(), handlerKey, ds)
			Expect(err).ToNot(HaveOccurred())
			Expect(envVariableStringPresent("PROBE_DNS_HOST", "google.com", ds.Spec.Template.Spec.Containers[0].Env)).To(BeTrue())
		})
	})

	Context("when network policies need to be deployed", func() {
		var (
			request ctrl.Request
		)
		BeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)

			objs := []runtime.Object{&nmstate}
			// Create a fake client to mock API calls.
			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl
			request.Name = existingNMStateName
			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
		It("should apply network policies", func() {
			netpols := &networkingv1.NetworkPolicyList{}
			err := cl.List(context.TODO(), netpols, client.InNamespace(handlerNamespace))
			Expect(err).ToNot(HaveOccurred())
			Expect(len(netpols.Items)).To(BeNumerically(">", 1))
		})
	})

	Context("Depending on cluster topology", func() {
		var (
			nodeSelector     map[string]string
			objects          []runtime.Object
			nodeTaints       []corev1.Taint
			infraTolerations []corev1.Toleration
		)

		BeforeEach(func() {
			objects = []runtime.Object{}
			nodeSelector = defaultHandlerNodeSelector
			nodeTaints = []corev1.Taint{
				{
					Key:    "node-role.kubernetes.io/control-plane",
					Effect: corev1.TaintEffectNoSchedule,
				},
			}
			infraTolerations = []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			}
		})

		JustBeforeEach(func() {
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NMState{},
			)
			nmstate.Spec.InfraNodeSelector = nodeSelector
			nmstate.Spec.InfraTolerations = infraTolerations
			objects = append(objects, &nmstate)

			cl = fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects...).Build()
			reconciler.Client = cl
			reconciler.APIClient = cl

			var request ctrl.Request
			request.Name = existingNMStateName

			result, err := reconciler.Reconcile(context.Background(), request)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		Context("On single node cluster", func() {
			BeforeEach(func() {
				objects = append(objects, dummyNode("node1", nodeSelector, nodeTaints))
			})

			It("should have a minAvailable = 0 in the pod disruption budget", func() {
				pdb := policyv1.PodDisruptionBudget{}
				pdbKey := webhookKey

				err := cl.Get(context.TODO(), pdbKey, &pdb)
				Expect(err).ToNot(HaveOccurred())
				Expect(pdb.Spec.MinAvailable.IntValue()).To(BeEquivalentTo(0))
			})

			It("should have one replica of the webhook deployment", func() {
				deployment := &appsv1.Deployment{}
				err := cl.Get(context.TODO(), webhookKey, deployment)

				Expect(err).ToNot(HaveOccurred())
				Expect(*deployment.Spec.Replicas).To(BeEquivalentTo(1))
			})
		})

		Context("On multi node cluster", func() {
			When("When multiple nodes match the node selector and node taints", func() {
				BeforeEach(func() {
					objects = append(objects, dummyNode("node1", nodeSelector, nodeTaints))
					objects = append(objects, dummyNode("node2", nodeSelector, nodeTaints))
					objects = append(objects, dummyNode("node3", nodeSelector, nodeTaints))
				})

				It("should have a minAvailable = 1 in the pod disruption budget", func() {
					pdb := policyv1.PodDisruptionBudget{}
					pdbKey := webhookKey

					err := cl.Get(context.TODO(), pdbKey, &pdb)
					Expect(err).ToNot(HaveOccurred())
					Expect(pdb.Spec.MinAvailable.IntValue()).To(BeEquivalentTo(1))
				})

				It("should have two replica of the webhook deployment", func() {
					deployment := &appsv1.Deployment{}
					err := cl.Get(context.TODO(), webhookKey, deployment)

					Expect(err).ToNot(HaveOccurred())
					Expect(*deployment.Spec.Replicas).To(BeEquivalentTo(2))
				})
			})

			When("When only one node matches the node selector and taints", func() {
				BeforeEach(func() {
					dummyTaints := []corev1.Taint{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoSchedule}}
					objects = append(objects, dummyNode("node1", nodeSelector, nodeTaints))
					objects = append(objects, dummyNode("node2", map[string]string{"foo": "bar"}, nodeTaints))
					objects = append(objects, dummyNode("node3", nodeSelector, dummyTaints))
				})

				It("should have a minAvailable = 0 in the pod disruption budget", func() {
					pdb := policyv1.PodDisruptionBudget{}
					pdbKey := webhookKey

					err := cl.Get(context.TODO(), pdbKey, &pdb)
					Expect(err).ToNot(HaveOccurred())
					Expect(pdb.Spec.MinAvailable.IntValue()).To(BeEquivalentTo(0))
				})

				It("should have one replica of the webhook deployment", func() {
					deployment := &appsv1.Deployment{}
					err := cl.Get(context.TODO(), webhookKey, deployment)

					Expect(err).ToNot(HaveOccurred())
					Expect(*deployment.Spec.Replicas).To(BeEquivalentTo(1))
				})
			})
		})
	})
})

func dummyNode(name string, labels map[string]string, taints []corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			Taints: taints,
		},
	}
}

func copyManifest(src, dst string) error {
	var err error
	var srcfd *os.File
	var dstfd *os.File
	var srcinfo os.FileInfo

	dstDir := dst
	fileName := ""
	dstIsAManifest := strings.HasSuffix(dst, ".yaml")
	if dstIsAManifest {
		dstDir, fileName = filepath.Split(dstDir)
	}

	// create dst directory if needed
	if _, err = os.Stat(dstDir); os.IsNotExist(err) {
		if err = os.MkdirAll(dstDir, os.ModePerm); err != nil {
			return err
		}
	}
	if fileName == "" {
		_, fileName = filepath.Split(src)
	}
	dst = filepath.Join(dstDir, fileName)
	if srcfd, err = os.Open(src); err != nil {
		return err
	}
	defer srcfd.Close()

	if dstfd, err = os.Create(dst); err != nil {
		return err
	}
	defer dstfd.Close()

	if _, err = io.Copy(dstfd, srcfd); err != nil {
		return err
	}
	if srcinfo, err = os.Stat(src); err != nil {
		return err
	}
	return os.Chmod(dst, srcinfo.Mode())
}

func copyManifests(manifestsDir string) error {
	srcToDest := map[string]string{
		"../../deploy/crds/nmstate.io_nodenetworkconfigurationenactments.yaml": "kubernetes-nmstate/crds/",
		"../../deploy/crds/nmstate.io_nodenetworkconfigurationpolicies.yaml":   "kubernetes-nmstate/crds/",
		"../../deploy/crds/nmstate.io_nodenetworkstates.yaml":                  "kubernetes-nmstate/crds/",
		"../../deploy/handler/namespace.yaml":                                  "kubernetes-nmstate/namespace/",
		"../../deploy/handler/network_policy.yaml":                             "kubernetes-nmstate/netpol/handler.yaml",
		"../../deploy/handler/operator.yaml":                                   "kubernetes-nmstate/handler/handler.yaml",
		"../../deploy/handler/service_account.yaml":                            "kubernetes-nmstate/rbac/",
		"../../deploy/handler/role.yaml":                                       "kubernetes-nmstate/rbac/",
		"../../deploy/handler/role_binding.yaml":                               "kubernetes-nmstate/rbac/",
	}

	for src, dest := range srcToDest {
		if err := copyManifest(src, filepath.Join(manifestsDir, dest)); err != nil {
			return err
		}
	}
	return nil
}

func checkTolerationInList(toleration corev1.Toleration, tolerationList []corev1.Toleration) bool {
	found := false
	for _, currentToleration := range tolerationList {
		if isSuperset(toleration, currentToleration) {
			found = true
			break
		}
	}
	return found
}

// isSuperset checks whether ss tolerates a superset of t.
func isSuperset(ss, t corev1.Toleration) bool {
	if apiequality.Semantic.DeepEqual(&t, &ss) {
		return true
	}

	if !isKeyMatching(t, ss) {
		return false
	}

	if !isEffectMatching(t, ss) {
		return false
	}

	if ss.Effect == corev1.TaintEffectNoExecute {
		if ss.TolerationSeconds != nil {
			if t.TolerationSeconds == nil ||
				*t.TolerationSeconds > *ss.TolerationSeconds {
				return false
			}
		}
	}

	switch ss.Operator {
	case corev1.TolerationOpEqual, "": // empty operator means Equal
		return t.Operator == corev1.TolerationOpEqual && t.Value == ss.Value
	case corev1.TolerationOpExists:
		return true
	default:
		return false
	}
}

// allTolerationsPresent check if all tolerations from toBeCheckedTolerations are superseded by actualTolerations.
func allTolerationsPresent(toBeCheckedTolerations, actualTolerations []corev1.Toleration) bool {
	tolerationsFound := true
	for _, toleration := range toBeCheckedTolerations {
		tolerationsFound = tolerationsFound && checkTolerationInList(toleration, actualTolerations)
	}
	return tolerationsFound
}

// anyTolerationsPresent check whether any tolerations from toBeCheckedTolerations are part of actualTolerations.
func anyTolerationsPresent(toBeCheckedTolerations, actualTolerations []corev1.Toleration) bool {
	tolerationsFound := false
	for _, toleration := range toBeCheckedTolerations {
		tolerationsFound = tolerationsFound || checkTolerationInList(toleration, actualTolerations)
	}
	return tolerationsFound
}

// isKeyMatching check if tolerations arguments match the toleration keys.
func isKeyMatching(a, b corev1.Toleration) bool {
	if a.Key == b.Key || (b.Key == "" && b.Operator == corev1.TolerationOpExists) {
		return true
	}
	return false
}

var _ = Describe("NMState controller apply function", func() {
	var (
		reconciler NMStateReconciler
		ctx        context.Context
		testScheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		testScheme = runtime.NewScheme()

		// Add core Kubernetes types to the scheme
		corev1.AddToScheme(testScheme)
		appsv1.AddToScheme(testScheme)

		reconciler = NMStateReconciler{
			Scheme: testScheme,
			Log:    ctrl.Log.WithName("test"),
		}
	})

	Describe("convertToTypedObjects", func() {
		Context("when converting a known type (ConfigMap)", func() {
			It("should successfully convert unstructured to typed objects", func() {
				// Create unstructured ConfigMap objects
				oldConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "test-config",
							"namespace": "test-ns",
						},
						"data": map[string]interface{}{
							"key1": "value1",
						},
					},
				}

				newConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "test-config",
							"namespace": "test-ns",
						},
						"data": map[string]interface{}{
							"key1": "value1",
							"key2": "value2",
						},
					},
				}

				typedOld, typedNew, err := reconciler.convertToTypedObjects(oldConfigMap, newConfigMap)

				Expect(err).ToNot(HaveOccurred())
				Expect(typedOld).ToNot(BeNil())
				Expect(typedNew).ToNot(BeNil())

				// Verify the typed objects are ConfigMaps
				oldCM, ok := typedOld.(*corev1.ConfigMap)
				Expect(ok).To(BeTrue())
				Expect(oldCM.Name).To(Equal("test-config"))
				Expect(oldCM.Data["key1"]).To(Equal("value1"))

				newCM, ok := typedNew.(*corev1.ConfigMap)
				Expect(ok).To(BeTrue())
				Expect(newCM.Name).To(Equal("test-config"))
				Expect(newCM.Data["key1"]).To(Equal("value1"))
				Expect(newCM.Data["key2"]).To(Equal("value2"))
			})
		})

		Context("when converting an unknown type", func() {
			It("should return an error", func() {
				unknownObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "unknown.io/v1",
						"kind":       "UnknownResource",
						"metadata": map[string]interface{}{
							"name": "test",
						},
					},
				}

				_, _, err := reconciler.convertToTypedObjects(unknownObj, unknownObj)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("scheme doesn't recognize GVK"))
			})
		})

		Context("when object has no GroupVersionKind", func() {
			It("should return an error", func() {
				emptyGVKObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "test",
						},
					},
				}

				_, _, err := reconciler.convertToTypedObjects(emptyGVKObj, emptyGVKObj)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("object has no GroupVersionKind set"))
			})
		})
	})

	Describe("preserveMetadata", func() {
		Context("when working with Namespace objects", func() {
			It("should merge existing and new labels", func() {
				oldNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
							"labels": map[string]interface{}{
								"existing-label":  "existing-value",
								"common-label":    "old-value",
								"custom.io/label": "custom-value",
							},
							"annotations": map[string]interface{}{
								"existing-annotation": "existing-value",
								"common-annotation":   "old-value",
							},
						},
					},
				}

				newNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
							"labels": map[string]interface{}{
								"new-label":    "new-value",
								"common-label": "new-value",
							},
							"annotations": map[string]interface{}{
								"new-annotation":    "new-value",
								"common-annotation": "new-value",
							},
						},
					},
				}

				err := reconciler.preserveMetadata(oldNamespace, newNamespace)

				Expect(err).ToNot(HaveOccurred())

				// Check merged labels
				labels := newNamespace.GetLabels()
				Expect(labels).To(HaveLen(4))
				Expect(labels["existing-label"]).To(Equal("existing-value")) // preserved
				Expect(labels["new-label"]).To(Equal("new-value"))           // added
				Expect(labels["common-label"]).To(Equal("new-value"))        // new takes precedence
				Expect(labels["custom.io/label"]).To(Equal("custom-value"))  // preserved

				// Check merged annotations
				annotations := newNamespace.GetAnnotations()
				Expect(annotations).To(HaveLen(3))
				Expect(annotations["existing-annotation"]).To(Equal("existing-value")) // preserved
				Expect(annotations["new-annotation"]).To(Equal("new-value"))           // added
				Expect(annotations["common-annotation"]).To(Equal("new-value"))        // new takes precedence
			})

			It("should handle empty labels and annotations gracefully", func() {
				oldNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
						},
					},
				}

				newNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
							"labels": map[string]interface{}{
								"new-label": "new-value",
							},
						},
					},
				}

				err := reconciler.preserveMetadata(oldNamespace, newNamespace)

				Expect(err).ToNot(HaveOccurred())

				labels := newNamespace.GetLabels()
				Expect(labels).To(HaveLen(1))
				Expect(labels["new-label"]).To(Equal("new-value"))
			})

			It("should preserve existing labels when new object has no labels", func() {
				oldNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
							"labels": map[string]interface{}{
								"existing-label": "existing-value",
							},
						},
					},
				}

				newNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-ns",
						},
					},
				}

				err := reconciler.preserveMetadata(oldNamespace, newNamespace)

				Expect(err).ToNot(HaveOccurred())

				labels := newNamespace.GetLabels()
				Expect(labels).To(HaveLen(1))
				Expect(labels["existing-label"]).To(Equal("existing-value"))
			})
		})

		Context("when working with non-Namespace objects", func() {
			It("should not modify metadata for other resource types", func() {
				oldConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "test-cm",
							"labels": map[string]interface{}{
								"existing-label": "existing-value",
							},
						},
					},
				}

				newConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "test-cm",
							"labels": map[string]interface{}{
								"new-label": "new-value",
							},
						},
					},
				}

				originalLabels := newConfigMap.GetLabels()

				err := reconciler.preserveMetadata(oldConfigMap, newConfigMap)

				Expect(err).ToNot(HaveOccurred())

				// Labels should remain unchanged for non-Namespace objects
				labels := newConfigMap.GetLabels()
				Expect(labels).To(Equal(originalLabels))
				Expect(labels).To(HaveLen(1))
				Expect(labels["new-label"]).To(Equal("new-value"))
			})
		})
	})

	Describe("apply function", func() {
		var (
			fakeClient client.Client
		)

		BeforeEach(func() {
			fakeClient = fake.NewClientBuilder().WithScheme(testScheme).Build()
			reconciler.Client = fakeClient
		})

		Context("when creating a new object", func() {
			It("should create the object successfully", func() {
				newConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "new-config",
							"namespace": "default",
						},
						"data": map[string]interface{}{
							"key": "value",
						},
					},
				}

				err := reconciler.apply(ctx, newConfigMap)

				Expect(err).ToNot(HaveOccurred())

				// Verify the object was created
				created := &corev1.ConfigMap{}
				key := client.ObjectKey{Name: "new-config", Namespace: "default"}
				err = fakeClient.Get(ctx, key, created)
				Expect(err).ToNot(HaveOccurred())
				Expect(created.Data["key"]).To(Equal("value"))
			})
		})

		Context("when updating an existing namespace with custom labels", func() {
			It("should preserve existing custom labels and annotations", func() {
				// Create initial Namespace with custom labels and annotations
				initialNamespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
						Labels: map[string]string{
							"custom.io/label": "custom-value",
							"existing-label":  "existing-value",
							"name":            "test-namespace", // This will be overridden
						},
						Annotations: map[string]string{
							"custom.io/annotation": "custom-annotation-value",
							"existing-annotation":  "existing-annotation-value",
						},
					},
				}
				err := fakeClient.Create(ctx, initialNamespace)
				Expect(err).ToNot(HaveOccurred())

				// Update with unstructured namespace object (simulating manifest application)
				updatedNamespace := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "test-namespace",
							"labels": map[string]interface{}{
								"name":                               "test-namespace",
								"pod-security.kubernetes.io/enforce": "privileged",
								"pod-security.kubernetes.io/audit":   "privileged",
								"pod-security.kubernetes.io/warn":    "privileged",
								"openshift.io/cluster-monitoring":    "true",
							},
						},
					},
				}

				err = reconciler.apply(ctx, updatedNamespace)

				Expect(err).ToNot(HaveOccurred())

				// Verify the namespace was updated and custom metadata was preserved
				updated := &corev1.Namespace{}
				key := client.ObjectKey{Name: "test-namespace"}
				err = fakeClient.Get(ctx, key, updated)
				Expect(err).ToNot(HaveOccurred())

				// Check that new labels from manifest are present
				Expect(updated.Labels["name"]).To(Equal("test-namespace"))
				Expect(updated.Labels["pod-security.kubernetes.io/enforce"]).To(Equal("privileged"))
				Expect(updated.Labels["pod-security.kubernetes.io/audit"]).To(Equal("privileged"))
				Expect(updated.Labels["pod-security.kubernetes.io/warn"]).To(Equal("privileged"))
				Expect(updated.Labels["openshift.io/cluster-monitoring"]).To(Equal("true"))

				// Check that existing custom labels are preserved
				Expect(updated.Labels["custom.io/label"]).To(Equal("custom-value"))
				Expect(updated.Labels["existing-label"]).To(Equal("existing-value"))

				// Check that existing custom annotations are preserved
				Expect(updated.Annotations["custom.io/annotation"]).To(Equal("custom-annotation-value"))
				Expect(updated.Annotations["existing-annotation"]).To(Equal("existing-annotation-value"))
			})
		})

		Context("when updating an existing object with known type", func() {
			It("should use strategic merge patch", func() {
				// Create initial ConfigMap
				initialConfigMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-config",
						Namespace: "default",
					},
					Data: map[string]string{
						"key1": "value1",
						"key3": "value3",
					},
				}
				err := fakeClient.Create(ctx, initialConfigMap)
				Expect(err).ToNot(HaveOccurred())

				// Update with unstructured object
				updatedConfigMap := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "existing-config",
							"namespace": "default",
						},
						"data": map[string]interface{}{
							"key1": "updated-value1",
							"key2": "value2",
						},
					},
				}

				err = reconciler.apply(ctx, updatedConfigMap)

				Expect(err).ToNot(HaveOccurred())

				// Verify the object was updated with strategic merge
				updated := &corev1.ConfigMap{}
				key := client.ObjectKey{Name: "existing-config", Namespace: "default"}
				err = fakeClient.Get(ctx, key, updated)
				Expect(err).ToNot(HaveOccurred())
				Expect(updated.Data["key1"]).To(Equal("updated-value1"))
				Expect(updated.Data["key2"]).To(Equal("value2"))
				// key3 should be preserved due to strategic merge behavior
			})
		})

		Context("when updating an existing object with unknown type", func() {
			It("should fall back to regular merge patch", func() {
				// Create initial object as unstructured (simulating unknown type)
				initialObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "unknown.io/v1",
						"kind":       "UnknownResource",
						"metadata": map[string]interface{}{
							"name":      "test-unknown",
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"field1": "value1",
						},
					},
				}
				err := fakeClient.Create(ctx, initialObj)
				Expect(err).ToNot(HaveOccurred())

				// Update the object
				updatedObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "unknown.io/v1",
						"kind":       "UnknownResource",
						"metadata": map[string]interface{}{
							"name":      "test-unknown",
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"field1": "updated-value1",
							"field2": "value2",
						},
					},
				}

				err = reconciler.apply(ctx, updatedObj)

				Expect(err).ToNot(HaveOccurred())

				// Verify the object was updated
				updated := &unstructured.Unstructured{}
				updated.SetGroupVersionKind(initialObj.GroupVersionKind())
				key := client.ObjectKey{Name: "test-unknown", Namespace: "default"}
				err = fakeClient.Get(ctx, key, updated)
				Expect(err).ToNot(HaveOccurred())

				spec, found, err := unstructured.NestedMap(updated.Object, "spec")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(spec["field1"]).To(Equal("updated-value1"))
				Expect(spec["field2"]).To(Equal("value2"))
			})
		})

		Context("when client operations fail", func() {
			It("should return appropriate errors for Get failures", func() {
				// Use a client that will fail on Get operations
				failingClient := &failingFakeClient{
					Client:    fakeClient,
					failOnGet: true,
				}
				reconciler.Client = failingClient

				testObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "test",
							"namespace": "default",
						},
					},
				}

				err := reconciler.apply(ctx, testObj)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("simulated Get failure"))
			})

			It("should return appropriate errors for Create failures", func() {
				failingClient := &failingFakeClient{
					Client:       fakeClient,
					failOnCreate: true,
				}
				reconciler.Client = failingClient

				testObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "test",
							"namespace": "default",
						},
					},
				}

				err := reconciler.apply(ctx, testObj)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed creating"))
			})

			It("should return appropriate errors for Patch failures", func() {
				// Create initial object
				initialObj := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
				}
				err := fakeClient.Create(ctx, initialObj)
				Expect(err).ToNot(HaveOccurred())

				failingClient := &failingFakeClient{
					Client:      fakeClient,
					failOnPatch: true,
				}
				reconciler.Client = failingClient

				testObj := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "test",
							"namespace": "default",
						},
					},
				}

				err = reconciler.apply(ctx, testObj)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed patching"))
			})
		})
	})
})

// failingFakeClient is a test helper that simulates client failures
type failingFakeClient struct {
	client.Client
	failOnGet    bool
	failOnCreate bool
	failOnPatch  bool
}

func (f *failingFakeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if f.failOnGet {
		return fmt.Errorf("simulated Get failure")
	}
	return f.Client.Get(ctx, key, obj, opts...)
}

func (f *failingFakeClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if f.failOnCreate {
		return fmt.Errorf("simulated Create failure")
	}
	return f.Client.Create(ctx, obj, opts...)
}

func (f *failingFakeClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if f.failOnPatch {
		return fmt.Errorf("simulated Patch failure")
	}
	return f.Client.Patch(ctx, obj, patch, opts...)
}

// isEffectMatching check if tolerations arguments match the effects
func isEffectMatching(a, b corev1.Toleration) bool {
	// An empty effect means match all effects.
	return a.Effect == b.Effect || b.Effect == ""
}

// envVariableStringPresent checks if specified env variable KEY and VALUE are present in the provided list
func envVariableStringPresent(key, value string, envList []corev1.EnvVar) bool {
	for _, env := range envList {
		if env.Name == key && env.Value == value {
			return true
		}
	}
	return false
}
