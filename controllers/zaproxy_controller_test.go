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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())

			zaproxy = newZAProxy(ZAProxyName, namespace)
			Expect(k8sClient.Create(ctx, zaproxy)).To(Succeed())
		})

		It("Should reconcile a ZAProxy and create necessary resources", func() {
			// Verify ConfigMap creation
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
				g.Expect(err).To(Succeed())
				g.Expect(cm.Data).To(HaveKey("af-plan.yaml"))
			}, timeout, interval).Should(Succeed())

			// Verify PVC creation
			Eventually(func(g Gomega) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-pvc", Namespace: namespace}, pvc)
				g.Expect(err).To(Succeed())
				g.Expect(pvc.Spec.StorageClassName).To(Equal(&zaproxy.Spec.StorageClassName))
				g.Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("1Gi")))
			}, timeout, interval).Should(Succeed())

			// Verify Service creation
			Eventually(func(g Gomega) {
				service := &corev1.Service{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName, Namespace: namespace}, service)
				g.Expect(err).To(Succeed())
				g.Expect(service.Spec.Ports).To(HaveLen(1))
				g.Expect(service.Spec.Ports[0].Port).To(Equal(int32(8080)))
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
				cm := &corev1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
				g.Expect(err).To(Succeed())

				expectedData := map[string]string{
					"af-plan.yaml": string(updatedPlanStr),
				}

				g.Expect(cm.Data).To(Equal(expectedData))
			}, timeout, interval).Should(Succeed())
		})

		It("Should delete the ZAProxy and all associated resources", func() {
			// Delete the ZAProxy
			Expect(k8sClient.Delete(ctx, zaproxy)).To(Succeed())

			// Verify ConfigMap deletion
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
				g.Expect(err).To(HaveOccurred())
			}, timeout, interval).Should(Succeed())

			// Verify PVC deletion
			Eventually(func(g Gomega) {
				pvc := &corev1.PersistentVolumeClaim{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-pvc", Namespace: namespace}, pvc)
				g.Expect(err).To(HaveOccurred())
			}, timeout, interval).Should(Succeed())

			// Verify Service deletion
			Eventually(func(g Gomega) {
				service := &corev1.Service{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName, Namespace: namespace}, service)
				g.Expect(err).To(HaveOccurred())
			}, timeout, interval).Should(Succeed())
		})

		It("Should clean up resources when ZAProxy is deleted", func() {
			zaproxyLookupKey := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
			createdZAProxy := &zaproxyorgv1alpha1.ZAProxy{}

			// Ensure ZAProxy is created
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			// Delete ZAProxy
			Expect(k8sClient.Delete(ctx, createdZAProxy)).To(Succeed())

			// Ensure ZAProxy is deleted
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}, timeout, interval).Should(Succeed())

			// Ensure ConfigMap is deleted
			cm := &corev1.ConfigMap{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-config", Namespace: namespace}, cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			// Ensure PVC is deleted
			pvc := &corev1.PersistentVolumeClaim{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-pvc", Namespace: namespace}, pvc)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("Should handle errors when ZAProxy resource is not found", func() {
			zaproxyLookupKey := types.NamespacedName{Name: "non-existent-zaproxy", Namespace: namespace}
			createdZAProxy := &zaproxyorgv1alpha1.ZAProxy{}

			err := k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("Should set the correct status conditions on ZAProxy", func() {
			zaproxyLookupKey := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
			createdZAProxy := &zaproxyorgv1alpha1.ZAProxy{}

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, zaproxyLookupKey, createdZAProxy)).To(Succeed())
				g.Expect(createdZAProxy.Status.Conditions).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			condition := createdZAProxy.Status.Conditions[0]
			Expect(condition.Type).To(Equal("Available"))
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})

func newZAProxy(name, namespace string) *zaproxyorgv1alpha1.ZAProxy {
	return &zaproxyorgv1alpha1.ZAProxy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "zaproxy.org/v1alpha1",
			Kind:       "ZAProxy",
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
