//go:build e2e

package e2e

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.opentelemetry.io/collector/pdata/pmetric"

	kitk8s "github.com/kyma-project/telemetry-manager/test/testkit/k8s"
	"github.com/kyma-project/telemetry-manager/test/testkit/k8s/verifiers"
	"github.com/kyma-project/telemetry-manager/test/testkit/kyma"
	kitmetric "github.com/kyma-project/telemetry-manager/test/testkit/kyma/telemetry/metric"
	. "github.com/kyma-project/telemetry-manager/test/testkit/matchers"
	"github.com/kyma-project/telemetry-manager/test/testkit/mocks"
	kitotlpmetric "github.com/kyma-project/telemetry-manager/test/testkit/otlp/metrics"
)

var _ = Describe("Metrics Prometheus Input", Label("metrics"), func() {
	Context("When a metricpipeline exists", Ordered, func() {
		var (
			pipelines          *kyma.PipelineList
			urls               *mocks.URLProvider
			mockDeploymentName = "metric-agent-receiver"
			mocksNs            = "metric-prometheus-input"
			metricGatewayName  = types.NamespacedName{Name: metricAgentGatewayBaseName, Namespace: kymaSystemNamespaceName}
			metricAgentName    = types.NamespacedName{Name: metricAgentBaseName, Namespace: kymaSystemNamespaceName}
		)

		BeforeAll(func() {
			k8sObjects, urlProvider, pipelinesProvider := makeMetricsPrometheusInputTestK8sObjects(mocksNs, mockDeploymentName)
			pipelines = pipelinesProvider
			urls = urlProvider

			DeferCleanup(func() {
				Expect(kitk8s.DeleteObjects(ctx, k8sClient, k8sObjects...)).Should(Succeed())
			})

			Expect(kitk8s.CreateObjects(ctx, k8sClient, k8sObjects...)).Should(Succeed())
		})

		It("Should have a running metric gateway deployment", func() {
			Eventually(func(g Gomega) {
				ready, err := verifiers.IsDeploymentReady(ctx, k8sClient, metricGatewayName)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(ready).To(BeTrue())
			}, timeout, interval).Should(Succeed())
		})

		It("Should have a metrics backend running", func() {
			Eventually(func(g Gomega) {
				key := types.NamespacedName{Name: mockDeploymentName, Namespace: mocksNs}
				ready, err := verifiers.IsDeploymentReady(ctx, k8sClient, key)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ready).To(BeTrue())
			}, timeout, interval).Should(Succeed())
		})

		It("Should have a running metric agent daemonset", func() {
			Eventually(func(g Gomega) {
				ready, err := verifiers.IsDaemonSetReady(ctx, k8sClient, metricAgentName)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(ready).To(BeTrue())
			}, timeout, interval).Should(Succeed())
		})

		It("Should have a running pipeline", func() {
			metricPipelineShouldBeRunning(pipelines.First())
		})

		It("Should verify custom metrics", func() {
			metricTypeMap := map[mocks.MetricType]pmetric.MetricType{
				mocks.MetricTypeCounter:   pmetric.MetricTypeSum,
				mocks.MetricTypeGauge:     pmetric.MetricTypeGauge,
				mocks.MetricTypeHistogram: pmetric.MetricTypeHistogram,
				mocks.MetricTypeSummary:   pmetric.MetricTypeSummary,
			}

			Eventually(func(g Gomega) {
				resp, err := proxyClient.Get(urls.MockBackendExport())
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(resp).To(HaveHTTPBody(SatisfyAll(
					ContainMetricsThatSatisfy(func(m pmetric.Metric) bool {
						return m.Name() == mocks.CustomMetricCPUTemperature.Name && m.Type() == metricTypeMap[mocks.CustomMetricCPUTemperature.Type]
					}),
					ContainMetricsThatSatisfy(func(m pmetric.Metric) bool {
						if m.Name() == mocks.CustomMetricHardDiskErrorsTotal.Name && m.Type() == metricTypeMap[mocks.CustomMetricHardDiskErrorsTotal.Type] && m.Sum().IsMonotonic() {
							return kitotlpmetric.AllDataPointsHaveAttributes(m, mocks.CustomMetricHardDiskErrorsTotal.Labels...)
						}
						return false
					}),
					ContainMetricsThatSatisfy(func(m pmetric.Metric) bool {
						if m.Name() == mocks.CustomMetricCPUEnergyHistogram.Name && m.Type() == metricTypeMap[mocks.CustomMetricCPUEnergyHistogram.Type] {
							return kitotlpmetric.AllDataPointsHaveAttributes(m, mocks.CustomMetricCPUEnergyHistogram.Labels...)
						}
						return false
					}),
					ContainMetricsThatSatisfy(func(m pmetric.Metric) bool {
						if m.Name() == mocks.CustomMetricHardwareHumidity.Name && m.Type() == metricTypeMap[mocks.CustomMetricHardwareHumidity.Type] {
							return kitotlpmetric.AllDataPointsHaveAttributes(m, mocks.CustomMetricHardwareHumidity.Labels...)
						}
						return false
					}))))
			}, timeout, interval).Should(Succeed())
		})

		It("Should verify no kubelet metrics", func() {
			Eventually(func(g Gomega) {
				resp, err := proxyClient.Get(urls.MockBackendExport())
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(resp).To(HaveHTTPBody(SatisfyAll(
					Not(ContainMetricsWithNames(kubeletMetricNames...)))))
			}, timeout, interval).Should(Succeed())
		})
	})
})

func makeMetricsPrometheusInputTestK8sObjects(mocksNamespaceName string, mockDeploymentName string) ([]client.Object, *mocks.URLProvider, *kyma.PipelineList) {
	var (
		objs         []client.Object
		pipelines    = kyma.NewPipelineList()
		urls         = mocks.NewURLProvider()
		grpcOTLPPort = 4317
		httpWebPort  = 80
	)

	mocksNamespace := kitk8s.NewNamespace(mocksNamespaceName)
	objs = append(objs, kitk8s.NewNamespace(mocksNamespaceName).K8sObject())

	// Mocks namespace objects.
	mockBackend := mocks.NewBackend(mockDeploymentName, mocksNamespace.Name(), "/metrics/"+telemetryDataFilename, mocks.SignalTypeMetrics)
	mockBackendConfigMap := mockBackend.ConfigMap("metric-receiver-config")
	mockBackendDeployment := mockBackend.Deployment(mockBackendConfigMap.Name())
	mockBackendExternalService := mockBackend.ExternalService().
		WithPort("grpc-otlp", grpcOTLPPort).
		WithPort("http-web", httpWebPort)
	mockMetricProvider := mocks.NewCustomMetricProvider(mocksNamespaceName)

	// Default namespace objects.
	otlpEndpointURL := mockBackendExternalService.OTLPEndpointURL(grpcOTLPPort)
	hostSecret := kitk8s.NewOpaqueSecret("metric-rcv-hostname", defaultNamespaceName, kitk8s.WithStringData("metric-host", otlpEndpointURL))
	metricPipeline := kitmetric.NewPipeline("pipeline-with-prometheus-input-enabled", hostSecret.SecretKeyRef("metric-host")).PrometheusInput(true)
	pipelines.Append(metricPipeline.Name())

	objs = append(objs, []client.Object{
		mockBackendConfigMap.K8sObject(),
		mockBackendDeployment.K8sObject(kitk8s.WithLabel("app", mockBackend.Name())),
		mockBackendExternalService.K8sObject(kitk8s.WithLabel("app", mockBackend.Name())),
		mockMetricProvider.K8sObject(),
		hostSecret.K8sObject(),
		metricPipeline.K8sObject(),
	}...)

	urls.SetMockBackendExport(proxyClient.ProxyURLForService(mocksNamespace.Name(), mockBackend.Name(), telemetryDataFilename, httpWebPort))

	return objs, urls, pipelines
}