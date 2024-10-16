package controllers

import (
	"context"
	"time"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ZAProxy controller", func() {

	const (
		ZAProxyName      = "test-zaproxy"
		ZAProxyNamespace = "default"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a ZAProxy", func() {
		It("Should get created", func() {
			By("By creating a new ZAProxy")
			ctx := context.Background()
			zaproxy := &zaproxyorgv1alpha1.ZAProxy{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "zaproxy.org/v1alpha1",
					Kind:       "ZAPRoxy",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      ZAProxyName,
					Namespace: ZAProxyNamespace,
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
			Expect(k8sClient.Create(ctx, zaproxy)).To(Succeed())

			zaproxyLookupKey := types.NamespacedName{Name: ZAProxyName, Namespace: ZAProxyNamespace}
			createdZAProxy := &zaproxyorgv1alpha1.ZAProxy{}

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)).To(Succeed())
			}, timeout, interval).Should(Succeed())
			Expect(createdZAProxy.Spec.StorageClassName).To(Equal("standard"))
		})
	})
})
