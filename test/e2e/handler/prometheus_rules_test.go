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
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nmstate/kubernetes-nmstate/test/cmd"
	testenv "github.com/nmstate/kubernetes-nmstate/test/env"
)

// PrometheusQueryResponse represents the response from Prometheus query API
type PrometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// queryPrometheus queries the Prometheus API for a given PromQL expression
func queryPrometheus(token, query string) (*PrometheusQueryResponse, error) {
	const (
		prometheusPod = "prometheus-k8s-0"
		container     = "prometheus"
	)

	encodedQuery := url.QueryEscape(query)
	curlCmd := fmt.Sprintf("curl -s -k --header 'Authorization: Bearer %s' --header 'X-Authorization-Classification: notsecret' 'https://localhost:9090/api/v1/query?query=%s'", token, encodedQuery)

	output, err := cmd.Kubectl("exec", "-n", testenv.MonitoringNamespace, prometheusPod, "-c", container, "--", "sh", "-c", curlCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to query prometheus: %w", err)
	}

	var response PrometheusQueryResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return nil, fmt.Errorf("failed to parse prometheus response: %w, output: %s", err, output)
	}

	return &response, nil
}

// getRecordingRuleValue queries a recording rule and returns its value as a float
func getRecordingRuleValue(token, ruleName string) (float64, error) {
	response, err := queryPrometheus(token, ruleName)
	if err != nil {
		return 0, err
	}

	if response.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed with status: %s", response.Status)
	}

	if len(response.Data.Result) == 0 {
		return 0, fmt.Errorf("no results for recording rule: %s", ruleName)
	}

	// Sum all values if there are multiple results (e.g., per-node metrics)
	var total float64
	for _, result := range response.Data.Result {
		if len(result.Value) < 2 {
			continue
		}
		valueStr, ok := result.Value[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			continue
		}
		total += value
	}

	return total, nil
}

// recordingRuleExists checks if a recording rule returns any data
func recordingRuleExists(token, ruleName string) bool {
	response, err := queryPrometheus(token, ruleName)
	if err != nil {
		return false
	}
	return response.Status == "success" && len(response.Data.Result) > 0
}

// getRecordingRuleLabels returns the labels present in the recording rule results
func getRecordingRuleLabels(token, ruleName string) ([]map[string]string, error) {
	response, err := queryPrometheus(token, ruleName)
	if err != nil {
		return nil, err
	}

	if response.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed with status: %s", response.Status)
	}

	var labels []map[string]string
	for _, result := range response.Data.Result {
		labels = append(labels, result.Metric)
	}
	return labels, nil
}

var _ = Describe("Prometheus Recording Rules", func() {
	var token string

	BeforeEach(func() {
		var err error
		token, err = getPrometheusToken()
		Expect(err).ToNot(HaveOccurred())
	})

	Context("kubernetes_nmstate_features_applied rules", func() {
		It("should have cluster:kubernetes_nmstate_features_applied:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "cluster:kubernetes_nmstate_features_applied:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule cluster:kubernetes_nmstate_features_applied:sum should exist")
		})

		It("should aggregate by name label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "cluster:kubernetes_nmstate_features_applied:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(Or(
					BeEmpty(), // No features applied yet is valid
					ContainElement(HaveKey("name")),
				))
		})
	})

	Context("kubernetes_nmstate_network_interfaces rules", func() {
		It("should have cluster:kubernetes_nmstate_network_interfaces:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "cluster:kubernetes_nmstate_network_interfaces:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule cluster:kubernetes_nmstate_network_interfaces:sum should exist")
		})

		It("should have node:kubernetes_nmstate_network_interfaces:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "node:kubernetes_nmstate_network_interfaces:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule node:kubernetes_nmstate_network_interfaces:sum should exist")
		})

		It("should have node:kubernetes_nmstate_network_interfaces_by_type:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "node:kubernetes_nmstate_network_interfaces_by_type:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule node:kubernetes_nmstate_network_interfaces_by_type:sum should exist")
		})

		It("cluster rule should aggregate by type label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "cluster:kubernetes_nmstate_network_interfaces:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(HaveKey("type")))
		})

		It("node rule should aggregate by node label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "node:kubernetes_nmstate_network_interfaces:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(HaveKey("node")))
		})

		It("node by type rule should aggregate by both node and type labels", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "node:kubernetes_nmstate_network_interfaces_by_type:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(And(HaveKey("node"), HaveKey("type"))))
		})

		It("should return positive values for interface counts", func() {
			Eventually(func() (float64, error) {
				return getRecordingRuleValue(token, "cluster:kubernetes_nmstate_network_interfaces:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeNumerically(">", 0), "cluster should have at least one network interface")
		})
	})

	Context("kubernetes_nmstate_routes rules", func() {
		It("should have cluster:kubernetes_nmstate_routes_by_type:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "cluster:kubernetes_nmstate_routes_by_type:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule cluster:kubernetes_nmstate_routes_by_type:sum should exist")
		})

		It("should have cluster:kubernetes_nmstate_routes_by_ip_stack:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "cluster:kubernetes_nmstate_routes_by_ip_stack:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule cluster:kubernetes_nmstate_routes_by_ip_stack:sum should exist")
		})

		It("should have node:kubernetes_nmstate_routes:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "node:kubernetes_nmstate_routes:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule node:kubernetes_nmstate_routes:sum should exist")
		})

		It("should have node:kubernetes_nmstate_routes_by_ip_stack:sum recording rule", func() {
			Eventually(func() bool {
				return recordingRuleExists(token, "node:kubernetes_nmstate_routes_by_ip_stack:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "recording rule node:kubernetes_nmstate_routes_by_ip_stack:sum should exist")
		})

		It("routes by type rule should aggregate by type label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "cluster:kubernetes_nmstate_routes_by_type:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(HaveKey("type")))
		})

		It("routes by ip_stack rule should aggregate by ip_stack label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "cluster:kubernetes_nmstate_routes_by_ip_stack:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(HaveKey("ip_stack")))
		})

		It("node routes rule should aggregate by node label", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "node:kubernetes_nmstate_routes:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(HaveKey("node")))
		})

		It("node routes by ip_stack rule should aggregate by both node and ip_stack labels", func() {
			Eventually(func() ([]map[string]string, error) {
				return getRecordingRuleLabels(token, "node:kubernetes_nmstate_routes_by_ip_stack:sum")
			}).
				WithPolling(5 * time.Second).
				WithTimeout(2 * time.Minute).
				Should(ContainElement(And(HaveKey("node"), HaveKey("ip_stack"))))
		})

		It("should return positive values for route counts", func() {
			Eventually(func() (float64, error) {
				return getRecordingRuleValue(token, "cluster:kubernetes_nmstate_routes_by_type:sum")
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeNumerically(">", 0), "cluster should have at least one route")
		})
	})

	Context("recording rules consistency", func() {
		It("sum of node interfaces should equal cluster interface count per type", func() {
			Eventually(func() bool {
				// Get cluster-wide sum by type
				clusterResponse, err := queryPrometheus(token, "cluster:kubernetes_nmstate_network_interfaces:sum")
				if err != nil || clusterResponse.Status != "success" {
					return false
				}

				// Get node-level sums
				nodeByTypeResponse, err := queryPrometheus(token, "node:kubernetes_nmstate_network_interfaces_by_type:sum")
				if err != nil || nodeByTypeResponse.Status != "success" {
					return false
				}

				// Build map of type -> count from cluster rule
				clusterCounts := make(map[string]float64)
				for _, result := range clusterResponse.Data.Result {
					ifaceType := result.Metric["type"]
					if len(result.Value) >= 2 {
						if valueStr, ok := result.Value[1].(string); ok {
							if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
								clusterCounts[ifaceType] = value
							}
						}
					}
				}

				// Build map of type -> count from summing node-level metrics
				nodeSumCounts := make(map[string]float64)
				for _, result := range nodeByTypeResponse.Data.Result {
					ifaceType := result.Metric["type"]
					if len(result.Value) >= 2 {
						if valueStr, ok := result.Value[1].(string); ok {
							if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
								nodeSumCounts[ifaceType] += value
							}
						}
					}
				}

				// Verify counts match
				for ifaceType, clusterCount := range clusterCounts {
					if nodeSumCounts[ifaceType] != clusterCount {
						return false
					}
				}

				return len(clusterCounts) > 0
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "cluster and node interface counts should be consistent")
		})

		It("sum of node routes should equal cluster route count per type", func() {
			Eventually(func() bool {
				// Get cluster-wide sum by type
				clusterResponse, err := queryPrometheus(token, "cluster:kubernetes_nmstate_routes_by_type:sum")
				if err != nil || clusterResponse.Status != "success" {
					return false
				}

				// Get node-level sums
				nodeResponse, err := queryPrometheus(token, "node:kubernetes_nmstate_routes:sum")
				if err != nil || nodeResponse.Status != "success" {
					return false
				}

				// Calculate cluster total from type breakdown
				var clusterTotal float64
				for _, result := range clusterResponse.Data.Result {
					if len(result.Value) >= 2 {
						if valueStr, ok := result.Value[1].(string); ok {
							if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
								clusterTotal += value
							}
						}
					}
				}

				// Calculate node total
				var nodeTotal float64
				for _, result := range nodeResponse.Data.Result {
					if len(result.Value) >= 2 {
						if valueStr, ok := result.Value[1].(string); ok {
							if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
								nodeTotal += value
							}
						}
					}
				}

				// Verify totals match
				return clusterTotal > 0 && clusterTotal == nodeTotal
			}).
				WithPolling(5*time.Second).
				WithTimeout(2*time.Minute).
				Should(BeTrue(), "cluster and node route counts should be consistent")
		})
	})
})
