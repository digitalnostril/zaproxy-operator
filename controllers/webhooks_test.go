package controllers

import (
	"context"
	"fmt"
	"time"

	zaproxyorgv1alpha1 "github.com/digitalnostril/zaproxy-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kbatch "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Webhooks", func() {

	const (
		ZAProxyName = "test-zaproxy"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

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

	Context("CreateJob", func() {
		It("Should create a Job for the ZAProxy resource", func() {
			namespacedName := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
			result, err := CreateJob(ctx, k8sClient, namespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			job := &kbatch.Job{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-job", Namespace: namespace}, job)
				g.Expect(err).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("EndDelayZAPJob", func() {
		It("Should end delay job successfully", func() {
			namespacedName := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
			result, err := CreateJob(ctx, k8sClient, namespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			result, err = EndDelayZAPJob(ctx, k8sClient, namespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})
	})

	// Context("When deleting a ZAProxy", func() {
	// 	It("Should delete the Job for the ZAProxy resource", func() {
	// 		namespacedName := types.NamespacedName{Name: ZAProxyName, Namespace: namespace}
	// 		result, err := CreateJob(ctx, k8sClient, namespacedName)
	// 		Expect(err).NotTo(HaveOccurred())
	// 		Expect(result).To(Equal(ctrl.Result{}))

	// 		job := &kbatch.Job{}
	// 		Eventually(func(g Gomega) {
	// 			err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-job", Namespace: namespace}, job)
	// 			g.Expect(err).To(Succeed())
	// 		}, timeout, interval).Should(Succeed())

	// 		Expect(k8sClient.Delete(ctx, zaproxy)).To(Succeed())

	// 		Eventually(func(g Gomega) {
	// 			err := k8sClient.Get(ctx, types.NamespacedName{Name: ZAProxyName + "-job", Namespace: namespace}, job)
	// 			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	// 		}, timeout, interval).Should(Succeed())
	// 	})
	// })
})
