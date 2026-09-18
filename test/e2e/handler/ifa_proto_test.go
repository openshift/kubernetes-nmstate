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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Other-protocol IP addresses", func() {
	const (
		managedV4   = "192.0.2.251"
		extraV4     = "192.0.2.253"
		prefixV4    = "24"
		managedV6   = "2001:db8::1:1"
		extraV6     = "2001:db8::1:3"
		prefixV6    = "64"
		destV4      = "203.0.113.0/24"
		nextHopV4   = "192.0.2.1"
		destV6      = "2001:dc8::/64"
		nextHopV6   = "2001:db8::1:2"
		extraV4CIDR = extraV4 + "/" + prefixV4
		extraV6CIDR = extraV6 + "/" + prefixV6
	)

	var node string

	BeforeEach(func() {
		node = nodes[0]
		updateDesiredStateAtNodeAndWait(
			node,
			ifaceUpWithDualStackStaticIP(firstSecondaryNic, managedV4, prefixV4, managedV6, prefixV6),
		)
		addKernelAddr(node, firstSecondaryNic, extraV4CIDR, ifaProtOVNK)
		addKernelAddr(node, firstSecondaryNic, extraV6CIDR, ifaProtOVNK)
		skipIfKernelLacksIFAProto(node, extraV4)
	})

	AfterEach(func() {
		deleteKernelAddr(node, firstSecondaryNic, extraV4CIDR)
		deleteKernelAddr(node, firstSecondaryNic, extraV6CIDR)
		updateDesiredStateAndWait(ifaceIPAndRoutesAbsent(firstSecondaryNic))
		ipAddressForNodeInterfaceEventually(node, firstSecondaryNic).Should(BeEmpty())
		resetDesiredStateForNodes()
	})

	It("should not persist other-protocol addresses into NM when applying routes", func() {
		updateDesiredStateAtNodeAndWait(
			node,
			routesOnlyOnIface(firstSecondaryNic, destV4, nextHopV4, destV6, nextHopV6),
		)

		assertOtherProtocolKeptOnIface(node, firstSecondaryNic, extraV4, extraV6, managedV4, managedV6)
		routeNextHopInterface(node, destV4).Should(Equal(firstSecondaryNic))
		routeNextHopInterface(node, destV6).Should(Equal(firstSecondaryNic))
	})
})

func assertOtherProtocolKeptOnIface(node, iface, extraV4, extraV6, managedV4, managedV6 string) {
	GinkgoHelper()
	infoV4 := kernelAddrInfo(node, extraV4)
	Expect(infoV4.Local).To(Equal(extraV4))
	Expect(normalizeAddrProtocol(infoV4.Protocol)).To(Equal(ifaProtOVNKHex))
	infoV6 := kernelAddrInfo(node, extraV6)
	Expect(infoV6.Local).To(Equal(extraV6))
	Expect(normalizeAddrProtocol(infoV6.Protocol)).To(Equal(ifaProtOVNKHex))

	ipv4NM := nmDeviceAddresses(node, iface, "ipv4")
	Expect(ipv4NM).To(ContainSubstring(managedV4))
	Expect(ipv4NM).NotTo(ContainSubstring(extraV4))
	ipv6NM := nmDeviceAddresses(node, iface, "ipv6")
	Expect(ipv6NM).To(ContainSubstring(managedV6))
	Expect(ipv6NM).NotTo(ContainSubstring(extraV6))

	Eventually(func() string {
		return nnsAddressProtocol(node, iface, extraV4, "ipv4")
	}, ReadTimeout, ReadInterval).Should(Equal(ifaProtOVNKHex))
	Eventually(func() string {
		return nnsAddressProtocol(node, iface, extraV6, "ipv6")
	}, ReadTimeout, ReadInterval).Should(Equal(ifaProtOVNKHex))
}
