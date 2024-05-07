//go:build e2e

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/telemetry-manager/internal/conditions"
	"github.com/kyma-project/telemetry-manager/internal/testutils"
	kitk8s "github.com/kyma-project/telemetry-manager/test/testkit/k8s"
	"github.com/kyma-project/telemetry-manager/test/testkit/mocks/backend"
	"github.com/kyma-project/telemetry-manager/test/testkit/mocks/telemetrygen"
	"github.com/kyma-project/telemetry-manager/test/testkit/suite"
	"github.com/kyma-project/telemetry-manager/test/testkit/verifiers"
)

var _ = Describe(suite.ID(), Label(suite.LabelTraces), func() {
	var (
		mockNs       = suite.ID()
		pipelineName = suite.ID()
	)

	makeResources := func() []client.Object {
		var objs []client.Object
		objs = append(objs, kitk8s.NewNamespace(mockNs).K8sObject())

		serverCertsDefault, clientCertsDefault, err := testutils.NewCertBuilder(backend.DefaultName, mockNs).Build()
		Expect(err).ToNot(HaveOccurred())

		_, clientCertsCreatedAgain, err := testutils.NewCertBuilder(backend.DefaultName, mockNs).Build()
		Expect(err).ToNot(HaveOccurred())

		backend := backend.New(mockNs, backend.SignalTypeTraces, backend.WithTLS(*serverCertsDefault))
		objs = append(objs, backend.K8sObjects()...)

		invalidClientCerts := &testutils.ClientCerts{
			CaCertPem:     clientCertsDefault.CaCertPem,
			ClientCertPem: clientCertsDefault.ClientCertPem,
			ClientKeyPem:  clientCertsCreatedAgain.ClientKeyPem,
		}

		pipeline := kitk8s.NewTracePipelineV1Alpha1(pipelineName).
			WithOutputEndpointFromSecret(backend.HostSecretRefV1Alpha1()).
			WithTLS(*invalidClientCerts)

		objs = append(objs, pipeline.K8sObject(),
			telemetrygen.New(mockNs, telemetrygen.SignalTypeTraces).K8sObject(),
		)

		return objs
	}

	Context("When a tracepipeline is configured TLS Cert that does not match the Key", Ordered, func() {
		BeforeAll(func() {
			k8sObjects := makeResources()

			DeferCleanup(func() {
				Expect(kitk8s.DeleteObjects(ctx, k8sClient, k8sObjects...)).Should(Succeed())
			})
			Expect(kitk8s.CreateObjects(ctx, k8sClient, k8sObjects...)).Should(Succeed())
		})

		It("Should not have running pipelines", func() {
			verifiers.TracePipelineShouldNotBeHealthy(ctx, k8sClient, pipelineName)
		})

		It("Should have a tls certificate key pair invalid condition set in pipeline conditions", func() {
			verifiers.TracePipelineShouldHaveTLSCondition(ctx, k8sClient, pipelineName, conditions.ReasonTLSCertificateKeyPairInvalid)
		})

		It("Should have telemetryCR showing tls certificate key pair invalid condition for trace component in its status", func() {
			verifiers.TelemetryShouldHaveCondition(ctx, k8sClient, "TraceComponentsHealthy", conditions.ReasonTLSCertificateKeyPairInvalid, false)
		})

	})
})