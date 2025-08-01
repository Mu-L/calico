// Copyright (c) 2022 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build fvtests

package fv_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	api "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/api/pkg/lib/numorstring"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/felix/fv/infrastructure"
	"github.com/projectcalico/calico/felix/fv/utils"
	"github.com/projectcalico/calico/felix/fv/workload"
	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	client "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
)

var _ = infrastructure.DatastoreDescribe("_BPF-SAFE_ Felix bpf test policy dump", []apiconfig.DatastoreType{apiconfig.EtcdV3}, func(getInfra infrastructure.InfraFactory) {

	if os.Getenv("FELIX_FV_ENABLE_BPF") != "true" {
		// Non-BPF run.
		return
	}

	var (
		infra        infrastructure.DatastoreInfra
		tc           infrastructure.TopologyContainers
		calicoClient client.Interface
		w            [2]*workload.Workload
	)

	BeforeEach(func() {
		infra = getInfra()
		opts := infrastructure.DefaultTopologyOptions()
		opts.ExtraEnvVars["FELIX_BPFPolicyDebugEnabled"] = "true"
		tc, calicoClient = infrastructure.StartNNodeTopology(1, opts, infra)
		for i := 0; i < 2; i++ {
			wIP := fmt.Sprintf("10.65.0.%d", i+2)
			w[i] = workload.Run(tc.Felixes[0], fmt.Sprintf("w%d", i), "default", wIP, "8055", "tcp")
			w[i].WorkloadEndpoint.Labels = map[string]string{"name": w[i].Name}
			w[i].ConfigureInInfra(infra)
		}
		ensureAllNodesBPFProgramsAttached(tc.Felixes)
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			tc.Felixes[0].Exec("calico-bpf", "policy", "dump", w[0].InterfaceName, "all")
			infra.DumpErrorData()
		}

		for i := 0; i < 2; i++ {
			w[i].Stop()
		}
		tc.Stop()
		infra.Stop()
	})

	createPolicy := func(policy *api.GlobalNetworkPolicy) *api.GlobalNetworkPolicy {
		log.WithField("policy", dumpResource(policy)).Info("Creating policy")
		policy, err := calicoClient.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
		Expect(err).NotTo(HaveOccurred())
		return policy
	}

	srcNets := []string{
		"11.0.0.8/32",
		"10.0.0.8/32",
	}

	dstNets := []string{
		"12.0.0.8/32",
		"13.0.0.8/32",
	}
	It("should dump policy debug information with TCP", func() {
		var err error
		protoTCP := numorstring.ProtocolFromString(numorstring.ProtocolTCP)
		protoUDP := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
		sportRange, err := numorstring.PortFromRange(100, 105)
		dportRange, err := numorstring.PortFromRange(200, 205)
		Expect(err).NotTo(HaveOccurred())

		pol := api.NewGlobalNetworkPolicy()
		pol.Namespace = "fv"
		pol.Name = "policy-tcp"
		pol.Spec.Ingress = []api.Rule{{Action: "Allow", Protocol: &protoTCP}}
		pol.Spec.Ingress[0].Source = api.EntityRule{Nets: srcNets, Ports: []numorstring.Port{numorstring.SinglePort(8055), sportRange}}
		pol.Spec.Ingress[0].Destination = api.EntityRule{Nets: dstNets, Ports: []numorstring.Port{numorstring.SinglePort(9055), dportRange}}
		pol.Spec.Ingress[0].Source.Selector = w[1].NameSelector()
		pol.Spec.Egress = []api.Rule{{Action: "Deny", Protocol: &protoUDP, NotProtocol: &protoTCP}}
		pol.Spec.Egress[0].Source = api.EntityRule{NotNets: srcNets, NotPorts: []numorstring.Port{numorstring.SinglePort(8055), sportRange}}
		pol.Spec.Egress[0].Destination = api.EntityRule{NotNets: dstNets, NotPorts: []numorstring.Port{numorstring.SinglePort(9055), dportRange}}
		pol.Spec.Egress[0].Destination.NotSelector = w[1].NameSelector()
		pol.Spec.Selector = w[0].NameSelector()
		pol = createPolicy(pol)
		out := ""
		ifaceStr := fmt.Sprintf("IfaceName: %s", w[0].InterfaceName)
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[0].InterfaceName, "ingress")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "10s", "200ms").Should(And(
			ContainSubstring("Start of policy default.policy-tcp"),
			ContainSubstring("IPSets src_ip_set_ids"),
		))

		outStr := out
		Expect(outStr).To(ContainSubstring("Start of rule action:\"allow\""))
		Expect(outStr).To(ContainSubstring("IPSets src_ip_set_ids:"))
		re := regexp.MustCompile("0x[0-9a-fA-F]+")
		ipSetFound := false
		for _, tmp := range strings.Split(outStr, "\n") {
			if strings.Contains(tmp, "IPSets src_ip_set_ids:") {
				log.WithField("line", tmp).Info("Examining line for IPSet ID")
				ipsetStr := re.FindAllString(tmp, -1)
				Expect(len(ipsetStr)).To(Equal(1))
				// IPSet ID is 64bit.
				log.WithField("ipsetStr", ipsetStr[0]).Info("Found IPSet ID")
				Expect(len(ipsetStr[0])).To(BeNumerically("<=", 18), "IPSet ID should be 64bit, not "+ipsetStr[0])
				// Vanishingly unlikely to be less than 10 characters.
				Expect(len(ipsetStr[0])).To(BeNumerically(">", 10), "IPSet ID should be 64bit, not "+ipsetStr[0])
				ipSetFound = true
			}
		}
		Expect(ipSetFound).To(BeTrue(), fmt.Sprintf("IP set was missing in output: %q", out))
		// check ingress policy dump with eBPF assembler code
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[0].InterfaceName, "ingress", "-a")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of tier default"))

		outStr = string(out)
		Expect(outStr).To(ContainSubstring(ifaceStr))
		Expect(outStr).To(ContainSubstring("Hook: tc egress"))
		Expect(outStr).To(ContainSubstring("Start of policy default.policy-tcp"))
		Expect(outStr).To(ContainSubstring("Load packet metadata saved by previous program"))
		Expect(outStr).To(ContainSubstring("Save state pointer in register R9"))
		Expect(outStr).To(ContainSubstring("If protocol != tcp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source not in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest not in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source port is not within any of {8055,100-105}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest port is not within any of {9055,200-205}, skip to next rule"))

		// check egress policy dump with eBPF assembler code
		out = ""
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[0].InterfaceName, "egress")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of policy default.policy-tcp"))

		outStr = string(out)
		Expect(outStr).To(ContainSubstring("Start of rule action:\"deny\""))
		ipSetFound = false
		for _, tmp := range strings.Split(outStr, "\n") {
			if strings.Contains(tmp, "IPSets not_dst_ip_set_ids:") {
				log.WithField("line", tmp).Info("Examining line for IPSet ID")
				ipsetStr := re.FindAllString(tmp, -1)
				Expect(len(ipsetStr)).To(Equal(1))
				// IPSet ID is 64bit.
				log.WithField("ipsetStr", ipsetStr[0]).Info("Found IPSet ID")
				Expect(len(ipsetStr[0])).To(BeNumerically("<=", 18), "IPSet ID should be 64bit, not "+ipsetStr[0])
				// Vanishingly unlikely to be less than 10 characters.
				Expect(len(ipsetStr[0])).To(BeNumerically(">", 10), "IPSet ID should be 64bit, not "+ipsetStr[0])
				ipSetFound = true
			}
		}
		Expect(ipSetFound).To(BeTrue())

		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[0].InterfaceName, "egress", "-a")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of tier default"))

		outStr = string(out)
		Expect(outStr).To(ContainSubstring(ifaceStr))
		Expect(outStr).To(ContainSubstring("Hook: tc ingress"))
		Expect(outStr).To(ContainSubstring("Start of policy default.policy-tcp"))
		Expect(outStr).To(ContainSubstring("Load packet metadata saved by previous program"))
		Expect(outStr).To(ContainSubstring("Save state pointer in register R9"))
		Expect(outStr).To(ContainSubstring("If protocol == tcp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source port is within any of {8055,100-105}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest port is within any of {9055,200-205}, skip to next rule"))

		// Test calico-bpf policy dump all with eBPF assembler code
		out = ""
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[0].InterfaceName, "all", "-a")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of tier default"))
		outStr = string(out)
		Expect(outStr).To(ContainSubstring("Hook: tc ingress"))
		Expect(outStr).To(ContainSubstring("Hook: tc egress"))
		Expect(outStr).To(ContainSubstring("If protocol == tcp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If protocol != tcp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source port is within any of {8055,100-105}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest port is within any of {9055,200-205}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source not in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest not in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source port is not within any of {8055,100-105}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest port is not within any of {9055,200-205}, skip to next rule"))
	})

	It("should dump policy debug information with ICMP", func() {
		var err error
		protoICMP := numorstring.ProtocolFromString(numorstring.ProtocolICMP)
		icmpType := 10
		icmpCode := 12
		icmpFields := &api.ICMPFields{Type: &icmpType, Code: &icmpCode}

		pol := api.NewGlobalNetworkPolicy()
		pol.Namespace = "fv"
		pol.Name = "policy-icmp"
		pol.Spec.Ingress = []api.Rule{{Action: "Allow", Protocol: &protoICMP, ICMP: icmpFields}}
		pol.Spec.Ingress[0].Source = api.EntityRule{Nets: srcNets}
		pol.Spec.Ingress[0].Destination = api.EntityRule{Nets: dstNets}
		pol.Spec.Egress = []api.Rule{{Action: "Deny", NotProtocol: &protoICMP, NotICMP: icmpFields}}
		pol.Spec.Egress[0].Source = api.EntityRule{NotNets: srcNets}
		pol.Spec.Egress[0].Destination = api.EntityRule{NotNets: dstNets}
		pol.Spec.Selector = w[1].NameSelector()

		pol = createPolicy(pol)
		out := ""
		ifaceStr := fmt.Sprintf("IfaceName: %s", w[1].InterfaceName)
		// check ingress policy dump with eBPF assembler code
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[1].InterfaceName, "ingress", "-a")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of tier default"))
		outStr := string(out)
		Expect(outStr).To(ContainSubstring(ifaceStr))
		Expect(outStr).To(ContainSubstring("Hook: tc egress"))
		Expect(outStr).To(ContainSubstring("Start of policy default.policy-icmp"))
		Expect(outStr).To(ContainSubstring("Load packet metadata saved by previous program"))
		Expect(outStr).To(ContainSubstring("Save state pointer in register R9"))
		Expect(outStr).To(ContainSubstring("If protocol != icmp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If ICMP type != 10 or code != 12, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source not in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest not in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))

		// check egress policy dump with eBPF assembler code
		out = ""
		Eventually(func() string {
			out, err = tc.Felixes[0].ExecOutput("calico-bpf", "policy", "dump", w[1].InterfaceName, "egress", "-a")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "5s", "200ms").Should(ContainSubstring("Start of tier default"))
		outStr = string(out)
		Expect(outStr).To(ContainSubstring(ifaceStr))
		Expect(outStr).To(ContainSubstring("Hook: tc ingress"))
		Expect(outStr).To(ContainSubstring("Start of policy default.policy-icmp"))
		Expect(outStr).To(ContainSubstring("Load packet metadata saved by previous program"))
		Expect(outStr).To(ContainSubstring("Save state pointer in register R9"))
		Expect(outStr).To(ContainSubstring("If protocol == icmp, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If ICMP type == 10 and code == 12, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If source in {11.0.0.8/32,10.0.0.8/32}, skip to next rule"))
		Expect(outStr).To(ContainSubstring("If dest in {12.0.0.8/32,13.0.0.8/32}, skip to next rule"))
	})
})
