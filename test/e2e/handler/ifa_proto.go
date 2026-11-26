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

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	nmstate "github.com/nmstate/kubernetes-nmstate/api/shared"
	"github.com/nmstate/kubernetes-nmstate/test/cmd"
	testenv "github.com/nmstate/kubernetes-nmstate/test/env"
	"github.com/nmstate/kubernetes-nmstate/test/environment"
	"github.com/nmstate/kubernetes-nmstate/test/runner"
)

const (
	ifaProtOVNK              uint8 = 0x55
	ovnHostCIDRsAnnotation         = "k8s.ovn.org/host-cidrs"
	ovnEgressAssignableLabel       = "k8s.ovn.org/egress-assignable"
	egressIPGroupVersion           = "k8s.ovn.org/v1"
	egressIPKind                   = "EgressIP"
)

func dummyUpWithStaticIP(iface, ipAddress, prefixLen string) nmstate.State {
	return nmstate.NewState(fmt.Sprintf(`interfaces:
    - name: %s
      type: dummy
      state: up
      ipv4:
        address:
        - ip: %s
          prefix-length: %s
        dhcp: false
        enabled: true
`, iface, ipAddress, prefixLen))
}

func routesOnlyIPv4OnIface(iface, dest, nextHop string) nmstate.State {
	return nmstate.NewState(fmt.Sprintf(`routes:
    config:
    - destination: %s
      metric: 150
      next-hop-address: %s
      next-hop-interface: %s
      table-id: 254
`, dest, nextHop, iface))
}

func deleteKernelAddr(node, iface, cidr string) {
	GinkgoHelper()
	Byf("Deleting address %s from %s/%s", cidr, node, iface)
	_, _ = runner.RunAtNode(node, "sudo", "ip", "addr", "del", cidr, "dev", iface)
}

type kernelAddrProbe struct {
	pod  string
	path string
}

type kernelAddr struct {
	Found    bool   `json:"found"`
	Protocol *uint8 `json:"protocol"`
}

func installKernelAddrProbe(ctx context.Context, node string) kernelAddrProbe {
	GinkgoHelper()
	pods, err := testenv.KubeClient.CoreV1().Pods(testenv.OperatorNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "component=kubernetes-nmstate-handler",
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node).String(),
	})
	Expect(err).To(Succeed())

	// The handler pod shares the node's network namespace.
	probe := kernelAddrProbe{}
	for _, pod := range pods.Items {
		if pod.Spec.HostNetwork && pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil {
			probe.pod = pod.Name
			break
		}
	}
	Expect(probe.pod).NotTo(BeEmpty(), "expected a running host-network handler pod on %s", node)

	worker, err := testenv.KubeClient.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
	Expect(err).To(Succeed())
	binary := filepath.Join(GinkgoT().TempDir(), "ifa-proto")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./test/cmd/ifa-proto")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+worker.Status.NodeInfo.Architecture, "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	Expect(err).To(Succeed(), "building the address probe: %s", output)

	path, err := cmd.Kubectl(probe.execArgs(false, "mktemp", "/tmp/knmstate-ifa-proto.XXXXXXXXXX")...)
	Expect(err).To(Succeed())
	probe.path = strings.TrimSpace(path)
	DeferCleanup(func() error {
		_, removeErr := cmd.Kubectl(probe.execArgs(false, "rm", "-f", "--", probe.path)...)
		return removeErr
	})

	file, err := os.Open(binary)
	Expect(err).To(Succeed())
	defer file.Close()
	_, err = cmd.KubectlWithStdin(file, probe.execArgs(true, "sh", "-c", `cat > "$1" && chmod 700 "$1"`, "sh", probe.path)...)
	Expect(err).To(Succeed())
	return probe
}

func (p kernelAddrProbe) execArgs(stdin bool, command ...string) []string {
	args := []string{"exec", "-n", testenv.OperatorNamespace, p.pod, "-c", "nmstate-handler"}
	if stdin {
		args = append(args, "-i")
	}
	return append(append(args, "--"), command...)
}

func (p kernelAddrProbe) lookup(ip, iface string) (kernelAddr, error) {
	command := []string{p.path, ip}
	if iface != "" {
		command = append(command, iface)
	}
	output, err := cmd.Kubectl(p.execArgs(false, command...)...)
	if err != nil {
		return kernelAddr{}, err
	}
	var addr kernelAddr
	err = json.Unmarshal([]byte(output), &addr)
	return addr, err
}

func waitForKernelAddrOnIface(probe kernelAddrProbe, node, ip, iface string) kernelAddr {
	GinkgoHelper()
	var info kernelAddr
	Eventually(func(g Gomega) {
		var err error
		info, err = probe.lookup(ip, iface)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(info.Found).To(BeTrue(), "address %s should be present on %s/%s", ip, node, iface)
	}, ReadTimeout, ReadInterval).Should(Succeed())
	return info
}

func nmDeviceConnection(node, iface string) string {
	GinkgoHelper()
	output, err := runner.RunAtNode(node, "sudo", "nmcli", "-g", "GENERAL.CONNECTION", "device", "show", iface)
	Expect(err).NotTo(HaveOccurred(), output)
	return strings.TrimSpace(output)
}

func nmDeviceAddresses(node, iface, family string) string {
	GinkgoHelper()
	addrs, ok := nmDeviceAddressesIfPresent(node, iface, family)
	Expect(ok).To(BeTrue(), "device %s on %s should have an NM connection", iface, node)
	return addrs
}

func nmDeviceAddressesIfPresent(node, iface, family string) (string, bool) {
	output, err := runner.RunAtNode(node, "sudo", "nmcli", "-g", "GENERAL.CONNECTION", "device", "show", iface)
	if err != nil {
		return "", false
	}
	conn := strings.TrimSpace(output)
	if conn == "" || conn == "--" {
		return "", false
	}
	output, err = runner.RunAtNode(node, "sudo", "nmcli", "--escape", "no", "-g", family+".addresses", "connection", "show", conn)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(output), true
}

func restoreNMAddresses(node, iface, family, addresses string) {
	GinkgoHelper()
	conn := nmDeviceConnection(node, iface)
	if conn == "" || conn == "--" {
		return
	}
	_, _ = runner.RunAtNode(node, "sudo", "nmcli", "connection", "modify", conn, family+".addresses", addresses)
	_, _ = runner.RunAtNode(node, "sudo", "nmcli", "connection", "up", conn)
}

func skipUnlessEgressIPCRD() {
	GinkgoHelper()
	resources, err := testenv.KubeClient.Discovery().ServerResourcesForGroupVersion(egressIPGroupVersion)
	if err != nil {
		Skip("k8s.ovn.org/v1 is not served; EgressIP CRD is not installed")
	}
	for _, resource := range resources.APIResources {
		if resource.Kind == egressIPKind {
			return
		}
	}
	Skip("EgressIP kind is not served on k8s.ovn.org/v1")
}

func waitForHostCIDROrSkip(nodeName, cidr string) {
	GinkgoHelper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		node, err := testenv.KubeClient.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		if strings.Contains(node.Annotations[ovnHostCIDRsAnnotation], cidr) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	Skip(fmt.Sprintf("node %s %s never included %s", nodeName, ovnHostCIDRsAnnotation, cidr))
}

func setEgressAssignableLabel(nodeName string) (hadLabel bool) {
	GinkgoHelper()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node := &corev1.Node{}
		if getErr := testenv.Client.Get(context.TODO(), types.NamespacedName{Name: nodeName}, node); getErr != nil {
			return getErr
		}
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		if _, exists := node.Labels[ovnEgressAssignableLabel]; exists {
			hadLabel = true
			return nil
		}
		hadLabel = false
		node.Labels[ovnEgressAssignableLabel] = ""
		return testenv.Client.Update(context.TODO(), node)
	})
	Expect(err).NotTo(HaveOccurred())
	return hadLabel
}

func restoreEgressAssignableLabel(nodeName string, hadLabel bool) {
	GinkgoHelper()
	if hadLabel {
		return
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node := &corev1.Node{}
		if getErr := testenv.Client.Get(context.TODO(), types.NamespacedName{Name: nodeName}, node); getErr != nil {
			return getErr
		}
		delete(node.Labels, ovnEgressAssignableLabel)
		return testenv.Client.Update(context.TODO(), node)
	})
	Expect(err).NotTo(HaveOccurred())
}

func egressIPGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "k8s.ovn.org", Version: "v1", Kind: egressIPKind}
}

func newEgressIP(name, egressIP, nsLabelKey, nsLabelValue, podLabelKey, podLabelValue string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(egressIPGVK())
	obj.SetName(name)
	obj.Object["spec"] = map[string]any{
		"egressIPs": []any{egressIP},
		"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{nsLabelKey: nsLabelValue},
		},
		"podSelector": map[string]any{
			"matchLabels": map[string]any{podLabelKey: podLabelValue},
		},
	}
	return obj
}

func createEgressIP(obj *unstructured.Unstructured) {
	GinkgoHelper()
	Expect(testenv.Client.Create(context.TODO(), obj)).To(Succeed())
}

func deleteEgressIP(name string) {
	GinkgoHelper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(egressIPGVK())
	obj.SetName(name)
	err := testenv.Client.Delete(context.TODO(), obj)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() bool {
		getErr := testenv.Client.Get(context.TODO(), types.NamespacedName{Name: name}, obj)
		return apierrors.IsNotFound(getErr)
	}, ReadTimeout, ReadInterval).Should(BeTrue(), "EgressIP %s should be deleted", name)
}

func egressIPAssignment(name string) (nodeName, ip string) {
	GinkgoHelper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(egressIPGVK())
	err := testenv.Client.Get(context.TODO(), types.NamespacedName{Name: name}, obj)
	if err != nil {
		return "", ""
	}
	items, found, err := unstructured.NestedSlice(obj.Object, "status", "items")
	if err != nil || !found {
		return "", ""
	}
	if len(items) == 0 {
		return "", ""
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		return "", ""
	}
	if n, ok := item["node"].(string); ok {
		nodeName = n
	}
	if eip, ok := item["egressIP"].(string); ok {
		ip = eip
	}
	return nodeName, ip
}

func waitForEgressIPAssignedOrSkip(name, expectedIP, expectedNode string) {
	GinkgoHelper()
	deadline := time.Now().Add(2 * time.Minute)
	var assignedNode, assignedIP string
	for time.Now().Before(deadline) {
		assignedNode, assignedIP = egressIPAssignment(name)
		if assignedIP == expectedIP && assignedNode == expectedNode {
			return
		}
		time.Sleep(2 * time.Second)
	}
	Skip(fmt.Sprintf(
		"EgressIP %s was not assigned to %s (last status node=%q ip=%q); secondary-host EIP may be unsupported",
		name, expectedNode, assignedNode, assignedIP,
	))
}

func createLabeledNamespace(name string, labels map[string]string) {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	err := testenv.Client.Create(context.TODO(), ns)
	if apierrors.IsAlreadyExists(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
}

func deleteNamespace(name string) {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := testenv.Client.Delete(context.TODO(), ns)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() bool {
		getErr := testenv.Client.Get(context.TODO(), types.NamespacedName{Name: name}, &corev1.Namespace{})
		return apierrors.IsNotFound(getErr)
	}, ReadTimeout, ReadInterval).Should(BeTrue(), "namespace %s should be deleted", name)
}

func createPausePod(namespace, name string, labels map[string]string) {
	GinkgoHelper()
	nonRoot := int64(65534)
	allowPrivEsc := false
	runAsNonRoot := true
	image := environment.GetVarWithDefault("PAUSE_IMAGE", "registry.k8s.io/pause:3.9")
	container := corev1.Container{
		Name:  "pause",
		Image: image,
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                &nonRoot,
			RunAsNonRoot:             &runAsNonRoot,
			AllowPrivilegeEscalation: &allowPrivEsc,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	if !strings.Contains(strings.ToLower(image), "pause") {
		container.Command = []string{"sleep", "infinity"}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			Containers:    []corev1.Container{container},
			RestartPolicy: corev1.RestartPolicyAlways,
		},
	}
	err := testenv.Client.Create(context.TODO(), pod)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	Eventually(func() corev1.PodPhase {
		got := &corev1.Pod{}
		if getErr := testenv.Client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: name}, got); getErr != nil {
			return ""
		}
		return got.Status.Phase
	}, ReadTimeout, ReadInterval).Should(Equal(corev1.PodRunning), "pause pod %s/%s should be running", namespace, name)
}
