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
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsutil "github.com/gardener/gardener/pkg/utils/secrets"
)

const (
	KubeApiServerSecretNameAggregatorCACertificate          = Prefix + "-kube-aggregator-ca"
	KubeApiServerSecretNameAggregatorClientCertificate      = Prefix + "-kube-aggregator"
	KubeApiServerSecretNameApiServerCACertificate           = Prefix + "-kube-apiserver-ca"
	KubeApiServerSecretNameApiServerServerCertificate       = Prefix + "-kube-apiserver"
	KubeApiServerSecretNameKubeControllerManagerCertificate = Prefix + "-kube-controller-manager"
	KubeApiServerSecretNameClientAdminCertificate           = Prefix + "-kubeconfig-for-admin"
	KubeApiServerSecretNameMetricsScraperCertificate        = Prefix + "-metrics-scraper"
)

func (o *operation) deployKubeAPIServerCertificates(ctx context.Context, loadbalancer string, checksums map[string]string) error {
	o.log.Infof("Deploying secrets containing kube-apiserver certificates")

	aggregatorCACertificate, err := o.deployKubeApiServerAggregatorCACertificate(ctx, checksums)
	if err != nil {
		return err
	}

	_, err = o.deployKubeApiServerAggregatorClientCertificate(ctx, aggregatorCACertificate, checksums)
	if err != nil {
		return err
	}

	apiServerCACertificate, err := o.deployKubeApiServerApiServerCACertificate(ctx, checksums)
	if err != nil {
		return err
	}

	_, err = o.deployKubeApiServerApiServerServerCertificate(ctx, apiServerCACertificate, loadbalancer, checksums)
	if err != nil {
		return err
	}

	_, err = o.deployKubeApiServerKubeControllerManagerClientCertificate(ctx, apiServerCACertificate, checksums)
	if err != nil {
		return err
	}

	_, err = o.deployKubeApiServerClientAdminCertificate(ctx, apiServerCACertificate, loadbalancer, checksums)
	if err != nil {
		return err
	}

	_, err = o.deployKubeApiServerMetricsScraperCertificate(ctx, apiServerCACertificate, checksums)
	if err != nil {
		return err
	}

	return nil
}

func (o *operation) deleteKubeAPIServerCertificates(ctx context.Context) error {
	o.log.Infof("Deleting secrets containing kube-apiserver certificates")

	for _, name := range []string{
		KubeApiServerSecretNameAggregatorCACertificate,
		KubeApiServerSecretNameAggregatorClientCertificate,
		KubeApiServerSecretNameApiServerCACertificate,
		KubeApiServerSecretNameApiServerServerCertificate,
		KubeApiServerSecretNameKubeControllerManagerCertificate,
		KubeApiServerSecretNameClientAdminCertificate,
		KubeApiServerSecretNameMetricsScraperCertificate,
	} {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.namespace}}
		if err := o.client.Delete(ctx, secret); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

func (o *operation) deployKubeApiServerApiServerCACertificate(ctx context.Context, checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameApiServerCACertificate,
		CertType:   secretsutil.CACert,
		CommonName: Prefix + ":ca:kube-apiserver",
	}

	cert, checksum, _, err := o.deployCertificate(ctx, certConfig, nil)
	if err != nil {
		return nil, err
	}

	o.exports.KubeApiserverCaPem = string(cert.CertificatePEM)

	checksums[ChecksumKeyKubeAPIServerCA] = checksum
	return cert, err
}

func (o *operation) deployKubeApiServerApiServerServerCertificate(ctx context.Context, caCertificate *secretsutil.Certificate,
	loadbalancer string, checksums map[string]string) (*secretsutil.Certificate, error) {
	dnsAccessDomain := o.imports.VirtualGarden.KubeAPIServer.DnsAccessDomain

	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameApiServerServerCertificate,
		CertType:   secretsutil.ServerCert,
		SigningCA:  caCertificate,
		CommonName: Prefix + ":server:kube-apiserver",
		DNSNames: []string{
			"localhost",
			KubeAPIServerServiceName,
			KubeAPIServerServiceName + ".garden",
			KubeAPIServerServiceName + ".garden.svc",
			KubeAPIServerServiceName + ".garden.svc.cluster",
			KubeAPIServerServiceName + ".garden.svc.cluster.local",
			"kubernetes",
			"kubernetes.default",
			"kubernetes.default.svc",
			"kubernetes.default.svc.cluster",
			"kubernetes.default.svc.cluster.local",
			fmt.Sprintf("api.%s", dnsAccessDomain),
			fmt.Sprintf("gardener.%s", dnsAccessDomain),
		},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("100.64.0.1"),
			net.ParseIP(loadbalancer),
		},
	}

	cert, checksum, _, err := o.deployCertificate(ctx, certConfig, nil)
	if err != nil {
		return nil, err
	}

	checksums[ChecksumKeyKubeAPIServerServer] = checksum
	return cert, err
}

func (o *operation) deployKubeApiServerKubeControllerManagerClientCertificate(ctx context.Context, caCertificate *secretsutil.Certificate, checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameKubeControllerManagerCertificate,
		CertType:   secretsutil.ClientCert,
		SigningCA:  caCertificate,
		CommonName: "system:kube-controller-manager",
	}

	kubeconfigGen := &kubeconfigGenerator{
		user:   "kube-controller-manager",
		server: "https://virtual-garden-kube-apiserver:443",
	}

	cert, checksum, _, err := o.deployCertificate(ctx, certConfig, kubeconfigGen)
	if err != nil {
		return nil, err
	}

	checksums[ChecksumKeyKubeControllerManagerClient] = checksum
	return cert, err
}

func (o *operation) deployKubeApiServerClientAdminCertificate(ctx context.Context, caCertificate *secretsutil.Certificate,
	loadBalancer string, checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameClientAdminCertificate,
		CertType:   secretsutil.ClientCert,
		SigningCA:  caCertificate,
		CommonName: Prefix + ":client:admin",
		Organization: []string{
			"system:masters",
		},
	}

	kubeconfigGen := &kubeconfigGenerator{
		user:   "admin",
		server: o.infrastructureProvider.GetKubeAPIServerURL(o.imports.VirtualGarden.KubeAPIServer, loadBalancer),
	}

	cert, _, kubeconfig, err := o.deployCertificate(ctx, certConfig, kubeconfigGen)
	if err != nil {
		return nil, err
	}

	o.exports.KubeconfigYaml = string(kubeconfig)

	return cert, err
}

func (o *operation) deployKubeApiServerMetricsScraperCertificate(
	ctx context.Context,
	caCertificate *secretsutil.Certificate,
	checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameMetricsScraperCertificate,
		CertType:   secretsutil.ClientCert,
		SigningCA:  caCertificate,
		CommonName: Prefix + ":client:metrics-scraper",
	}

	cert, _, _, err := o.deployCertificate(ctx, certConfig, nil)
	if err != nil {
		return nil, err
	}

	return cert, err
}

func (o *operation) deployKubeApiServerAggregatorCACertificate(ctx context.Context, checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameAggregatorCACertificate,
		CertType:   secretsutil.CACert,
		CommonName: Prefix + ":ca:kube-aggregator",
	}

	cert, checksum, _, err := o.deployCertificate(ctx, certConfig, nil)
	if err != nil {
		return nil, err
	}

	checksums[ChecksumKeyKubeAggregatorCA] = checksum
	return cert, err
}

func (o *operation) deployKubeApiServerAggregatorClientCertificate(ctx context.Context,
	caCertificate *secretsutil.Certificate, checksums map[string]string) (*secretsutil.Certificate, error) {
	certConfig := &secretsutil.CertificateSecretConfig{
		Name:       KubeApiServerSecretNameAggregatorClientCertificate,
		CertType:   secretsutil.ClientCert,
		SigningCA:  caCertificate,
		CommonName: Prefix + ":aggregator-client:kube-aggregator",
	}

	cert, checksum, _, err := o.deployCertificate(ctx, certConfig, nil)
	if err != nil {
		return nil, err
	}

	checksums[ChecksumKeyKubeAggregatorClient] = checksum
	return cert, err
}
