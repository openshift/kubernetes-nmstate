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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"syscall"
)

// IFA_PROTO is the u8 address protocol attribute in linux/if_addr.h.
const ifaProto = 11

type addressResult struct {
	Found    bool   `json:"found"`
	Protocol *uint8 `json:"protocol"`
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 || len(os.Args) > 3 {
		log.Fatal("usage: ifa-proto ADDRESS [INTERFACE]")
	}
	ip, err := netip.ParseAddr(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	var index uint32
	if len(os.Args) == 3 {
		link, linkErr := net.InterfaceByName(os.Args[2])
		if linkErr != nil {
			log.Fatal(linkErr)
		}
		index = uint32(link.Index)
	}
	result, err := lookupAddress(ip.Unmap(), index)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
}

func lookupAddress(ip netip.Addr, index uint32) (addressResult, error) {
	family := syscall.AF_INET6
	if ip.Is4() {
		family = syscall.AF_INET
	}
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETADDR, family)
	if err != nil {
		return addressResult{}, err
	}
	messages, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return addressResult{}, err
	}
	return findAddress(messages, ip, index)
}

func findAddress(messages []syscall.NetlinkMessage, ip netip.Addr, index uint32) (addressResult, error) {
	for _, message := range messages {
		if message.Header.Type != syscall.RTM_NEWADDR {
			continue
		}
		if len(message.Data) < syscall.SizeofIfAddrmsg {
			return addressResult{}, fmt.Errorf("short RTM_NEWADDR message")
		}
		if index != 0 && binary.NativeEndian.Uint32(message.Data[4:8]) != index {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&message)
		if err != nil {
			return addressResult{}, err
		}
		var local, address netip.Addr
		var protocol *uint8
		for _, attr := range attrs {
			switch attr.Attr.Type {
			case syscall.IFA_LOCAL:
				local, _ = netip.AddrFromSlice(attr.Value)
			case syscall.IFA_ADDRESS:
				address, _ = netip.AddrFromSlice(attr.Value)
			case ifaProto:
				if len(attr.Value) != 1 {
					return addressResult{}, fmt.Errorf("invalid IFA_PROTO length %d", len(attr.Value))
				}
				value := attr.Value[0]
				protocol = &value
			}
		}
		// IFA_ADDRESS can be the peer address on a point-to-point link.
		if !local.IsValid() {
			local = address
		}
		if local == ip {
			return addressResult{Found: true, Protocol: protocol}, nil
		}
	}
	return addressResult{}, nil
}
