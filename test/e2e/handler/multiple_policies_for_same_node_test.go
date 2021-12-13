package handler

import (
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NodeNetworkState", func() {
	Context("with multiple policies configured", func() {
		var (
			staticIPPolicy = "static-ip-policy"
			vlanPolicy     = "vlan-policy"
			ipAddress      = "10.244.0.1"
			vlanID         = "102"
			prefixLen      = "24"
		)

		BeforeEach(func() {
			setDesiredStateWithPolicy(staticIPPolicy, ifaceUpWithStaticIP(firstSecondaryNic, ipAddress, prefixLen))
			waitForAvailablePolicy(staticIPPolicy)
			setDesiredStateWithPolicy(vlanPolicy, ifaceUpWithVlanUp(firstSecondaryNic, vlanID))
			waitForAvailablePolicy(vlanPolicy)
		})

		AfterEach(func() {
			setDesiredStateWithPolicy(staticIPPolicy, ifaceDownIPv4Disabled(firstSecondaryNic))
			waitForAvailablePolicy(staticIPPolicy)
			setDesiredStateWithPolicy(vlanPolicy, vlanAbsent(firstSecondaryNic, vlanID))
			waitForAvailablePolicy(vlanPolicy)
			deletePolicy(staticIPPolicy)
			deletePolicy(vlanPolicy)
			resetDesiredStateForNodes()
		})

		It("should have the IP and vlan interface configured", func() {
			for _, node := range nodes {
				ipAddressForNodeInterfaceEventually(node, firstSecondaryNic).Should(Equal(ipAddress))
				interfacesNameForNodeEventually(node).Should(ContainElement(fmt.Sprintf(`%s.%s`, firstSecondaryNic, vlanID)))
				vlanForNodeInterfaceEventually(node, fmt.Sprintf(`%s.%s`, firstSecondaryNic, vlanID)).Should(Equal(vlanID))
			}
		})
	})
})
