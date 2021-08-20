// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package virtualgarden

import (
	"context"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	"github.com/gardener/virtual-garden/pkg/api"
	"github.com/gardener/virtual-garden/pkg/provider"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Deploy Item Controller Reconcile Test", func() {
	It("Should create api server secrets", func() {
		namespaceName := "test01"
		loadBalancer := "1.2.3.4"

		// checkSecret reads the secret with the specified name and checks that its data section contains the given keys.
		checkSecret := func(ctx context.Context, secretName string, keys ...string) {
			objectKey := client.ObjectKey{Name: secretName, Namespace: namespaceName}
			secret := &v1.Secret{}
			err := testenv.Client.Get(ctx, objectKey, secret)
			Expect(err).To(BeNil())
			for _, key := range keys {
				Expect(secret.Data).To(HaveKey(key))
			}
		}

		ctx := context.Background()
		defer ctx.Done()

		namespace := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
		}
		err := testenv.Client.Create(ctx, &namespace)
		Expect(err).To(BeNil())

		imports := getImports()

		infrastructureProvider, err := provider.NewInfrastructureProvider(api.InfrastructureProviderGCP)
		Expect(err).To(BeNil())

		operation := &operation{
			client:                 testenv.Client,
			log:                    testenv.Logger,
			infrastructureProvider: infrastructureProvider,
			backupProvider:         nil,
			namespace:              namespaceName,
			imports:                &imports,
			exports:                api.Exports{},
			imageRefs:              api.ImageRefs{},
		}

		// deploy certificates
		checksums1 := make(map[string]string)
		err = operation.deployKubeAPIServerCertificates(ctx, loadBalancer, checksums1)
		Expect(err).To(BeNil())

		checkSecret(ctx, KubeApiServerSecretNameApiServerCACertificate, "ca.key", "ca.crt")
		checkSecret(ctx, KubeApiServerSecretNameApiServerServerCertificate, "ca.crt", "tls.key", "tls.crt")
		checkSecret(ctx, KubeApiServerSecretNameKubeControllerManagerCertificate, "ca.crt", "tls.key", "tls.crt", "kubeconfig")
		checkSecret(ctx, KubeApiServerSecretNameAggregatorCACertificate, "ca.key", "ca.crt")
		checkSecret(ctx, KubeApiServerSecretNameAggregatorClientCertificate, "tls.key", "tls.crt")
		checkSecret(ctx, KubeApiServerSecretNameClientAdminCertificate, "ca.crt", "tls.key", "tls.crt", "kubeconfig")
		checkSecret(ctx, KubeApiServerSecretNameMetricsScraperCertificate, "tls.key", "tls.crt")

		Expect(checksums1).To(HaveKey(ChecksumKeyKubeAPIServerCA))
		Expect(checksums1).To(HaveKey(ChecksumKeyKubeAPIServerServer))
		Expect(checksums1).To(HaveKey(ChecksumKeyKubeControllerManagerClient))
		Expect(checksums1).To(HaveKey(ChecksumKeyKubeAggregatorCA))
		Expect(checksums1).To(HaveKey(ChecksumKeyKubeAggregatorClient))

		// redeploy and check that certificates remain unchanged
		checksums2 := make(map[string]string)
		Expect(operation.deployKubeAPIServerCertificates(ctx, loadBalancer, checksums2)).To(Succeed())
		Expect(checksums1).To(Equal(checksums2))

		// delete secrets and check that they are gone
		Expect(operation.deleteKubeAPIServerCertificates(ctx)).To(Succeed())
		secretList := &v1.SecretList{}
		Expect(testenv.Client.List(ctx, secretList)).To(Succeed())
		Expect(secretList.Items).To(BeEmpty())
	})
})

func getImports() api.Imports {
	return api.Imports{
		Cluster:        lsv1alpha1.Target{},
		HostingCluster: api.HostingCluster{},
		VirtualGarden: api.VirtualGarden{
			ETCD: nil,
			KubeAPIServer: &api.KubeAPIServer{
				Replicas:                 0,
				SNI:                      nil,
				DnsAccessDomain:          "com.our.test",
				GardenerControlplane:     api.GardenerControlplane{},
				AuditWebhookConfig:       api.AuditWebhookConfig{},
				AuditWebhookBatchMaxSize: "",
				SeedAuthorizer:           api.SeedAuthorizer{},
				HVPAEnabled:              false,
				HVPA:                     nil,
				EventTTL:                 nil,
				OidcIssuerURL:            nil,
				AdditionalVolumeMounts:   nil,
				AdditionalVolumes:        nil,
				HorizontalPodAutoscaler:  nil,
			},
			DeleteNamespace:   false,
			PriorityClassName: "",
		},
	}
}
