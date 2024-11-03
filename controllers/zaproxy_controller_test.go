package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ZAProxy controller", func() {

	const (
		ZAProxyName = "test-zaproxy"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a ZAProxy", func() {
		var (
			ctx       context.Context
			namespace string
			zaproxy   *zaproxyorgv1alpha1.ZAProxy
		)

		BeforeEach(func() {
			ctx = context.Background()
			namespace = fmt.Sprintf("test-namespace-%d-%d", time.Now().UnixNano(), GinkgoParallelProcess())

			ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())

			zaproxy = newZAProxy(ZAProxyName, namespace)
			Expect(k8sClient.Create(ctx, zaproxy)).To(Succeed())
		})

		It("Should get created", func() {
			zaproxyLookupKey := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
			createdZAProxy := &zaproxyorgv1alpha1.ZAProxy{}

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)).To(Succeed())
			}, timeout, interval).Should(Succeed())
			Expect(createdZAProxy.Spec.StorageClassName).To(Equal("standard"))
		})

		It("Should create a ConfigMap", func() {
			var plan map[string]interface{}
			json.Unmarshal(zaproxy.Spec.Automation.Plan.Raw, &plan)
			Expect(zaproxy).NotTo(BeNil())
			Expect(zaproxy.Spec.Automation.Plan.Raw).NotTo(BeEmpty())

			planStr, err := yaml.Marshal(plan)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				cm := &v1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
				g.Expect(err).To(Succeed())

				expectedData := map[string]string{
					"af-plan.yaml": string(planStr),
				}
				g.Expect(cm.Data).To(Equal(expectedData))
			}, timeout, interval).Should(Succeed())
		})

		It("Should update the ConfigMap when ZAProxy is updated", func() {
			// Prepare the expected updated ConfigMap data
			var currentPlan map[string]interface{}
			Expect(json.Unmarshal(zaproxy.Spec.Automation.Plan.Raw, &currentPlan)).To(Succeed())

			parameters, ok := currentPlan["plan"].(map[string]interface{})["env"].(map[string]interface{})["parameters"].(map[string]interface{})
			Expect(ok).To(BeTrue())

			parameters["failOnWarning"] = true

			updatedPlanRaw, err := json.Marshal(currentPlan)
			Expect(err).NotTo(HaveOccurred())
			updatedZAProxy := zaproxy.DeepCopy()
			updatedZAProxy.Spec.Automation.Plan.Raw = updatedPlanRaw

			patch := client.MergeFrom(zaproxy)

			// Patch the ZAProxy again with new automation plan
			Expect(k8sClient.Patch(ctx, updatedZAProxy, patch)).To(Succeed())

			// Check the ConfigMap reflects the updated plan
			updatedPlanStr, err := yaml.Marshal(currentPlan)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				cm := &v1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
				g.Expect(err).To(Succeed())

				expectedData := map[string]string{
					"af-plan.yaml": string(updatedPlanStr),
				}

				g.Expect(cm.Data).To(Equal(expectedData))
			}, timeout, interval).Should(Succeed())
		})
	})
})

func newZAProxy(name, namespace string) *zaproxyorgv1alpha1.ZAProxy {
	return &zaproxyorgv1alpha1.ZAProxy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "zaproxy.org/v1alpha1",
			Kind:       "ZAPRoxy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: zaproxyorgv1alpha1.ZAProxySpec{
			StorageClassName: "standard",
			Automation: zaproxyorgv1alpha1.Automation{
				Plan: runtime.RawExtension{
					Raw: []byte(`{
						"plan": {
							"env": {
								"contexts": [
									{
										"name": "Default Context",
										"urls": ["https://blog.digitalnostril.com"],
										"includePaths": [],
										"excludePaths": [],
										"authentication": {
											"parameters": {},
											"verification": {
												"method": "response",
												"pollFrequency": 60,
												"pollUnits": "requests"
											}
										},
										"sessionManagement": {
											"method": "cookie",
											"parameters": {}
										},
										"technology": {
											"exclude": []
										}
									}
								],
								"parameters": {
									"failOnError": true,
									"failOnWarning": false,
									"progressToStdout": true
								},
								"vars": {}
							},
							"jobs": [
								{
									"parameters": {
										"scanOnlyInScope": true,
										"enableTags": false,
										"disableAllRules": false
									},
									"rules": [],
									"name": "passiveScan-config",
									"type": "passiveScan-config"
								},
								{
									"parameters": {},
									"name": "spider",
									"type": "spider",
									"tests": [
										{
											"onFail": "INFO",
											"statistic": "automation.spider.urls.added",
											"site": "",
											"operator": ">=",
											"value": 100,
											"name": "At least 100 URLs found",
											"type": "stats"
										}
									]
								},
								{
									"parameters": {},
									"name": "passiveScan-wait",
									"type": "passiveScan-wait"
								},
								{
									"parameters": {
										"template": "risk-confidence-html",
										"reportDir": "/zap/reports",
										"reportTitle": "ZAP Scanning Report",
										"reportDescription": ""
									},
									"name": "report",
									"type": "report"
								},
								{
									"parameters": {
										"time": "600",
										"fileName": ""
									},
									"name": "delay",
									"type": "delay"
								}
							]
						}
					}`),
				},
			},
		},
	}
}
