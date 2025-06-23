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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/nmstate/kubernetes-nmstate/api/shared"
	nmstatev1 "github.com/nmstate/kubernetes-nmstate/api/v1"
	nmstatev1beta1 "github.com/nmstate/kubernetes-nmstate/api/v1beta1"
	"github.com/nmstate/kubernetes-nmstate/pkg/enactmentstatus/conditions"
)

var _ = Describe("NodeNetworkConfigurationPolicy controller predicates", func() {
	type predicateCase struct {
		GenerationOld   int64
		GenerationNew   int64
		ReconcileUpdate bool
	}
	DescribeTable("testing predicates",
		func(c predicateCase) {
			oldNNCP := nmstatev1.NodeNetworkConfigurationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation: c.GenerationOld,
				},
			}
			newNNCP := nmstatev1.NodeNetworkConfigurationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation: c.GenerationNew,
				},
			}

			predicate := onCreateOrUpdateWithDifferentGenerationOrDelete

			Expect(predicate.
				CreateFunc(event.TypedCreateEvent[*nmstatev1.NodeNetworkConfigurationPolicy]{
					Object: &newNNCP,
				})).To(BeTrue())
			Expect(predicate.
				UpdateFunc(event.TypedUpdateEvent[*nmstatev1.NodeNetworkConfigurationPolicy]{
					ObjectOld: &oldNNCP,
					ObjectNew: &newNNCP,
				})).To(Equal(c.ReconcileUpdate))
			Expect(predicate.
				DeleteFunc(event.TypedDeleteEvent[*nmstatev1.NodeNetworkConfigurationPolicy]{
					Object: &oldNNCP,
				})).To(BeTrue())
		},
		Entry("generation remains the same",
			predicateCase{
				GenerationOld:   1,
				GenerationNew:   1,
				ReconcileUpdate: false,
			}),
		Entry("generation is different",
			predicateCase{
				GenerationOld:   1,
				GenerationNew:   2,
				ReconcileUpdate: true,
			}),
	)

	type incrementUnavailableNodeCountCase struct {
		currentUnavailableNodeCount      int
		expectedUnavailableNodeCount     int
		expectedReconcileResult          ctrl.Result
		previousEnactmentConditions      func(*shared.ConditionList, string)
		shouldUpdateUnavailableNodeCount bool
	}
	DescribeTable("when claimNodeRunningUpdate is called and",
		func(c incrementUnavailableNodeCountCase) {
			nmstatectlShowFn = func() (string, error) { return "", nil }
			reconciler := NodeNetworkConfigurationPolicyReconciler{}
			s := scheme.Scheme
			s.AddKnownTypes(nmstatev1beta1.GroupVersion,
				&nmstatev1beta1.NodeNetworkState{},
				&nmstatev1beta1.NodeNetworkConfigurationEnactment{},
				&nmstatev1beta1.NodeNetworkConfigurationEnactmentList{},
			)
			s.AddKnownTypes(nmstatev1.GroupVersion,
				&nmstatev1.NodeNetworkConfigurationPolicy{},
			)

			node := corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}

			nns := nmstatev1beta1.NodeNetworkState{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
			}

			nncp := nmstatev1.NodeNetworkConfigurationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Status: shared.NodeNetworkConfigurationPolicyStatus{
					UnavailableNodeCount: c.currentUnavailableNodeCount,
				},
			}
			nnce := nmstatev1beta1.NodeNetworkConfigurationEnactment{
				ObjectMeta: metav1.ObjectMeta{
					Name: shared.EnactmentKey(nodeName, nncp.Name).Name,
				},
				Status: shared.NodeNetworkConfigurationEnactmentStatus{},
			}

			// simulate NNCE existnce/non-existence by setting conditions
			c.previousEnactmentConditions(&nnce.Status.Conditions, "")

			objs := []runtime.Object{&nncp, &nnce, &nns, &node}

			// Create a fake client to mock API calls.
			clb := fake.ClientBuilder{}
			clb.WithScheme(s)
			clb.WithRuntimeObjects(objs...)
			clb.WithStatusSubresource(&nncp)
			clb.WithStatusSubresource(&nnce)
			clb.WithStatusSubresource(&nns)
			cl := clb.Build()

			reconciler.Client = cl
			reconciler.APIClient = cl
			reconciler.Log = ctrl.Log.WithName("controllers").WithName("NodeNetworkConfigurationPolicy")

			res, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: nncp.Name},
			})

			Expect(err).To(BeNil())
			Expect(res).To(Equal(c.expectedReconcileResult))

			obtainedNNCP := nmstatev1.NodeNetworkConfigurationPolicy{}
			cl.Get(context.TODO(), types.NamespacedName{Name: nncp.Name}, &obtainedNNCP)
			Expect(obtainedNNCP.Status.UnavailableNodeCount).To(Equal(c.expectedUnavailableNodeCount))
			if c.shouldUpdateUnavailableNodeCount {
				Expect(obtainedNNCP.Status.LastUnavailableNodeCountUpdate).ToNot(BeNil())
			}
		},
		Entry("No node applying policy with empty enactment, should succeed incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      0,
				expectedUnavailableNodeCount:     0,
				previousEnactmentConditions:      func(*shared.ConditionList, string) {},
				expectedReconcileResult:          ctrl.Result{},
				shouldUpdateUnavailableNodeCount: true,
			}),
		Entry("No node applying policy with progressing enactment, should succeed incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      0,
				expectedUnavailableNodeCount:     0,
				previousEnactmentConditions:      conditions.SetProgressing,
				expectedReconcileResult:          ctrl.Result{},
				shouldUpdateUnavailableNodeCount: false,
			}),
		Entry("No node applying policy with Pending enactment, should succeed incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      0,
				expectedUnavailableNodeCount:     0,
				previousEnactmentConditions:      conditions.SetPending,
				expectedReconcileResult:          ctrl.Result{},
				shouldUpdateUnavailableNodeCount: true,
			}),
		Entry("One node applying policy with empty enactment, should conflict incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      1,
				expectedUnavailableNodeCount:     1,
				previousEnactmentConditions:      func(*shared.ConditionList, string) {},
				expectedReconcileResult:          ctrl.Result{RequeueAfter: nodeRunningUpdateRetryTime},
				shouldUpdateUnavailableNodeCount: false,
			}),
		Entry("One node applying policy with Progressing enactment, should succeed incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      1,
				expectedUnavailableNodeCount:     0,
				previousEnactmentConditions:      conditions.SetProgressing,
				expectedReconcileResult:          ctrl.Result{},
				shouldUpdateUnavailableNodeCount: false,
			}),
		Entry("One node applying policy with Pending enactment, should conflict incrementing UnavailableNodeCount",
			incrementUnavailableNodeCountCase{
				currentUnavailableNodeCount:      1,
				expectedUnavailableNodeCount:     1,
				previousEnactmentConditions:      conditions.SetPending,
				expectedReconcileResult:          ctrl.Result{RequeueAfter: nodeRunningUpdateRetryTime},
				shouldUpdateUnavailableNodeCount: false,
			}),
	)
})

var _ = Describe("NodeNetworkConfigurationPolicy controller finalizer logic", func() {
	var (
		reconciler NodeNetworkConfigurationPolicyReconciler
		cl         client.Client
		policy     *nmstatev1.NodeNetworkConfigurationPolicy
		node       *corev1.Node
		nns        *nmstatev1beta1.NodeNetworkState
		enactment  *nmstatev1beta1.NodeNetworkConfigurationEnactment
		s          *runtime.Scheme
	)

	BeforeEach(func() {
		// Mock external functions
		nmstatectlShowFn = func() (string, error) { return `{"interfaces": []}`, nil }
		nmstateRevertDesiredStateFn = func(client.Client, shared.State) (string, error) {
			return "revert success", nil
		}

		// Setup scheme
		s = scheme.Scheme
		s.AddKnownTypes(nmstatev1beta1.GroupVersion,
			&nmstatev1beta1.NodeNetworkState{},
			&nmstatev1beta1.NodeNetworkConfigurationEnactment{},
			&nmstatev1beta1.NodeNetworkConfigurationEnactmentList{},
		)
		s.AddKnownTypes(nmstatev1.GroupVersion,
			&nmstatev1.NodeNetworkConfigurationPolicy{},
		)

		// Create test objects
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}

		nns = &nmstatev1beta1.NodeNetworkState{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}

		policy = &nmstatev1.NodeNetworkConfigurationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-policy",
				Generation: 1,
			},
			Spec: shared.NodeNetworkConfigurationPolicySpec{
				DesiredState: shared.State{Raw: []byte(`{"interfaces": [{"name": "eth0", "type": "ethernet"}]}`)},
			},
		}

		enactment = &nmstatev1beta1.NodeNetworkConfigurationEnactment{
			ObjectMeta: metav1.ObjectMeta{
				Name:   shared.EnactmentKey(nodeName, policy.Name).Name,
				Labels: map[string]string{shared.EnactmentPolicyLabel: policy.Name},
			},
			Status: shared.NodeNetworkConfigurationEnactmentStatus{
				PolicyGeneration: policy.Generation,
				DesiredState:     policy.Spec.DesiredState,
			},
		}

		// Setup reconciler
		reconciler = NodeNetworkConfigurationPolicyReconciler{
			Log:    ctrl.Log.WithName("controllers").WithName("NodeNetworkConfigurationPolicy"),
			Scheme: s,
		}
	})

	Context("when policy is created without finalizer", func() {
		BeforeEach(func() {
			objs := []runtime.Object{policy, enactment, nns, node}
			clb := fake.ClientBuilder{}
			clb.WithScheme(s)
			clb.WithRuntimeObjects(objs...)
			clb.WithStatusSubresource(policy)
			clb.WithStatusSubresource(enactment)
			clb.WithStatusSubresource(nns)
			cl = clb.Build()

			reconciler.Client = cl
			reconciler.APIClient = cl
		})

		It("should add finalizer and return", func() {
			result, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: policy.Name},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify finalizer was added
			updatedPolicy := &nmstatev1.NodeNetworkConfigurationPolicy{}
			err = cl.Get(context.TODO(), types.NamespacedName{Name: policy.Name}, updatedPolicy)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedPolicy.Finalizers).To(ContainElement(NodeNetworkConfigurationPolicyFinalizerName))
		})
	})

	Context("when policy is being deleted", func() {
		BeforeEach(func() {
			// Set deletion timestamp and add finalizer
			now := metav1.Now()
			policy.DeletionTimestamp = &now
			policy.Finalizers = []string{NodeNetworkConfigurationPolicyFinalizerName}
		})

		Context("and finalizer removal fails", func() {
			BeforeEach(func() {
				objs := []runtime.Object{policy, enactment, nns, node}
				clb := fake.ClientBuilder{}
				clb.WithScheme(s)
				clb.WithRuntimeObjects(objs...)
				clb.WithStatusSubresource(policy)
				clb.WithStatusSubresource(enactment)
				clb.WithStatusSubresource(nns)
				cl = clb.Build()

				reconciler.Client = cl
				reconciler.APIClient = cl

				// Mock a client that fails on update
				reconciler.Client = &mockFailingClient{Client: cl}
			})

			It("should return error when finalizer removal fails", func() {
				result, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
					NamespacedName: types.NamespacedName{Name: policy.Name},
				})

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
			})
		})
	})

	Context("when policy deletion timestamp is zero but finalizer is already present", func() {
		BeforeEach(func() {
			// Add finalizer but no deletion timestamp
			policy.Finalizers = []string{NodeNetworkConfigurationPolicyFinalizerName}

			objs := []runtime.Object{policy, enactment, nns, node}
			clb := fake.ClientBuilder{}
			clb.WithScheme(s)
			clb.WithRuntimeObjects(objs...)
			clb.WithStatusSubresource(policy)
			clb.WithStatusSubresource(enactment)
			clb.WithStatusSubresource(nns)
			cl = clb.Build()

			reconciler.Client = cl
			reconciler.APIClient = cl
		})

		It("should proceed with normal reconciliation logic", func() {
			result, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: policy.Name},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify finalizer is still present
			updatedPolicy := &nmstatev1.NodeNetworkConfigurationPolicy{}
			err = cl.Get(context.TODO(), types.NamespacedName{Name: policy.Name}, updatedPolicy)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedPolicy.Finalizers).To(ContainElement(NodeNetworkConfigurationPolicyFinalizerName))
		})
	})
})

// mockFailingClient wraps a client and makes Update calls fail
type mockFailingClient struct {
	client.Client
}

func (m *mockFailingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return fmt.Errorf("mock update failure")
}

func (m *mockFailingClient) Status() client.StatusWriter {
	return m.Client.Status()
}
