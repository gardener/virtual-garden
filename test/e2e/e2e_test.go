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

package e2e_test

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gardener/virtual-garden/cmd/virtual-garden/app"
	"github.com/gardener/virtual-garden/pkg/api"
	"github.com/gardener/virtual-garden/pkg/api/helper"
	"github.com/gardener/virtual-garden/pkg/api/loader"
	"github.com/gardener/virtual-garden/pkg/provider"
	"github.com/gardener/virtual-garden/pkg/virtualgarden"

	secretsutil "github.com/gardener/gardener/pkg/utils/secrets"
	hvpav1alpha1 "github.com/gardener/hvpa-controller/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const handleETCDPersistentVolumes = true

var _ = Describe("VirtualGarden E2E tests", func() {
	var (
		ctx = context.Background()
		err error

		opts    *app.Options
		imports *api.Imports
		c       client.Client
	)

	BeforeSuite(func() {
		// Read options to figure out what is being tested.
		opts = app.NewOptions()
		opts.InitializeFromEnvironment()

		// Read imports to know what to verify the reconciliation.
		imports, err = loader.FromFile(opts.ImportsPath)
		Expect(err).To(BeNil())

		// Create Kubernetes client for actual verification calls in the hosting cluster.
		c, err = app.NewClientFromKubeconfig([]byte(imports.HostingCluster.Kubeconfig))
		Expect(err).To(BeNil())
	})

	AfterSuite(func() {
		origArgs := setCommandLineArguments()
		defer func() { os.Args = origArgs[:] }()

		By("Executing virtual garden deployer (deletion)")
		Expect(os.Setenv("OPERATION", "DELETE")).To(Succeed())
		Expect(app.NewCommandVirtualGarden().ExecuteContext(ctx)).To(Succeed())

		verifyDeletion(ctx, c, imports)
	})

	Describe("#NewCommandVirtualGarden.Execute()", func() {
		It("should correctly create/reconcile and delete the virtual garden (w/o namespace handling)", func() {
			origArgs := setCommandLineArguments()
			defer func() { os.Args = origArgs[:] }()

			if opts.OperationType != app.OperationTypeReconcile {
				return
			}

			By("Executing virtual garden deployer (reconciliation)")
			Expect(app.NewCommandVirtualGarden().ExecuteContext(ctx)).To(Succeed())

			verifyReconciliation(ctx, c, imports)
		})
	})
})

func verifyReconciliation(ctx context.Context, c client.Client, imports *api.Imports) {
	By("Checking that the kube-apiserver service was created as expected")
	kubeAPIServerService := &corev1.Service{}
	Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.KubeAPIServerServiceName, Namespace: imports.HostingCluster.Namespace}, kubeAPIServerService)).To(Succeed())

	Expect(kubeAPIServerService.Labels).To(HaveKeyWithValue("app", "virtual-garden"))
	Expect(kubeAPIServerService.Labels).To(HaveKeyWithValue("component", "kube-apiserver"))
	Expect(kubeAPIServerService.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
	Expect(kubeAPIServerService.Spec.ClusterIP).NotTo(BeEmpty())
	Expect(kubeAPIServerService.Spec.Selector).To(Equal(map[string]string{"app": "virtual-garden", "component": "kube-apiserver"}))
	Expect(kubeAPIServerService.Spec.Ports).To(HaveLen(1))
	Expect(kubeAPIServerService.Spec.Ports[0].Name).To(Equal("kube-apiserver"))
	Expect(kubeAPIServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
	Expect(kubeAPIServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(443)))
	Expect(kubeAPIServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
	Expect(kubeAPIServerService.Spec.Ports[0].NodePort).NotTo(BeZero())

	if helper.KubeAPIServerSNIEnabled(imports.VirtualGarden.KubeAPIServer) {
		Expect(kubeAPIServerService.Annotations).To(HaveKeyWithValue("dns.gardener.cloud/dnsnames", strings.Join(imports.VirtualGarden.KubeAPIServer.Exposure.SNI.Hostnames, ",")))
		if val := imports.VirtualGarden.KubeAPIServer.Exposure.SNI.DNSClass; val != nil {
			Expect(kubeAPIServerService.Annotations).To(HaveKeyWithValue("dns.gardener.cloud/class", val))
		}
		if val := imports.VirtualGarden.KubeAPIServer.Exposure.SNI.TTL; val != nil {
			Expect(kubeAPIServerService.Annotations).To(HaveKeyWithValue("dns.gardener.cloud/ttl", val))
		}
	} else {
		Expect(kubeAPIServerService.Annotations).NotTo(HaveKey("dns.gardener.cloud/dnsnames"))
		Expect(kubeAPIServerService.Annotations).NotTo(HaveKey("dns.gardener.cloud/class"))
		Expect(kubeAPIServerService.Annotations).NotTo(HaveKey("dns.gardener.cloud/ttl"))
	}

	By("Checking that the load balancer service was created successfully")
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
		service := &corev1.Service{}
		if err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.KubeAPIServerServiceName, Namespace: imports.HostingCluster.Namespace}, service); err != nil {
			return false, err
		}
		return len(service.Status.LoadBalancer.Ingress) > 0 && (service.Status.LoadBalancer.Ingress[0].Hostname != "" || service.Status.LoadBalancer.Ingress[0].IP != ""), nil
	}, timeoutCtx.Done())).To(Succeed())

	var (
		backupProvider provider.BackupProvider
		err            error
	)

	if helper.ETCDBackupEnabled(imports.VirtualGarden.ETCD) {
		By("Checking that the blob storage bucket for etcd backup was created successfully")
		backupProvider, err = provider.NewBackupProvider(imports.VirtualGarden.ETCD.Backup.InfrastructureProvider, imports.Credentials, imports.VirtualGarden.ETCD.Backup.CredentialsRef)
		Expect(err).NotTo(HaveOccurred())

		bucketExists, err := backupProvider.BucketExists(ctx, imports.VirtualGarden.ETCD.Backup.BucketName)
		Expect(err).NotTo(HaveOccurred())
		Expect(bucketExists).To(BeTrue())
	}

	By("Checking that the etcd storage class was created as expected")
	infrastructureProvider, err := provider.NewInfrastructureProvider(imports.HostingCluster.InfrastructureProvider)
	Expect(err).NotTo(HaveOccurred())

	etcdStorageClass := &storagev1.StorageClass{}
	Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStorageClassName(imports.VirtualGarden.ETCD)}, etcdStorageClass)).To(Succeed())

	provisioner, parameters := infrastructureProvider.ComputeStorageClassConfiguration()
	Expect(etcdStorageClass.Provisioner).To(Equal(provisioner))
	Expect(etcdStorageClass.Parameters).To(Equal(parameters))

	By("Checking that the etcd CA secret was created as expected")
	etcdSecretCACert := &corev1.Secret{}
	Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameCACertificate, Namespace: imports.HostingCluster.Namespace}, etcdSecretCACert)).To(Succeed())

	etcdCACertificate, err := secretsutil.LoadCertificate(etcdSecretCACert.Name, etcdSecretCACert.Data[secretsutil.DataKeyPrivateKeyCA], etcdSecretCACert.Data[secretsutil.DataKeyCertificateCA])
	Expect(err).NotTo(HaveOccurred())
	Expect(etcdCACertificate.Certificate.IsCA).To(BeTrue())
	Expect(etcdCACertificate.Certificate.Subject.CommonName).To(Equal("virtual-garden:ca:etcd"))
	Expect(etcdCACertificate.Certificate.KeyUsage).To(Equal(x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign | x509.KeyUsageCRLSign))

	By("Checking that the etcd client secret was created as expected")
	etcdSecretClientCert := &corev1.Secret{}
	Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameClientCertificate, Namespace: imports.HostingCluster.Namespace}, etcdSecretClientCert)).To(Succeed())

	etcdClientCertificate, err := secretsutil.LoadCertificate(etcdSecretClientCert.Name, etcdSecretClientCert.Data[secretsutil.DataKeyPrivateKey], etcdSecretClientCert.Data[secretsutil.DataKeyCertificate])
	Expect(err).NotTo(HaveOccurred())
	Expect(etcdClientCertificate.Certificate.IsCA).To(BeFalse())
	Expect(etcdClientCertificate.Certificate.Subject.CommonName).To(Equal("virtual-garden:client:etcd"))
	Expect(etcdClientCertificate.Certificate.Issuer.CommonName).To(Equal(etcdCACertificate.Certificate.Subject.CommonName))
	Expect(etcdClientCertificate.Certificate.KeyUsage).To(Equal(x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment))
	Expect(etcdClientCertificate.Certificate.ExtKeyUsage).To(Equal([]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}))

	if helper.ETCDBackupEnabled(imports.VirtualGarden.ETCD) {
		backupSecret := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameBackup, Namespace: imports.HostingCluster.Namespace}, backupSecret)).To(Succeed())
		_, secretData, _ := backupProvider.ComputeETCDBackupConfiguration(virtualgarden.ETCDVolumeMountPathBackupSecret)
		Expect(backupSecret.Data).To(Equal(secretData))
	}

	for _, role := range []string{virtualgarden.ETCDRoleMain, virtualgarden.ETCDRoleEvents} {
		By("Checking that the etcd service was created as expected (" + role + ")")
		etcdServiceService := &corev1.Service{}
		Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDServiceName(role), Namespace: imports.HostingCluster.Namespace}, etcdServiceService)).To(Succeed())

		Expect(etcdServiceService.Labels).To(HaveKeyWithValue("app", "virtual-garden"))
		Expect(etcdServiceService.Labels).To(HaveKeyWithValue("component", "etcd"))
		Expect(etcdServiceService.Labels).To(HaveKeyWithValue("role", role))
		Expect(etcdServiceService.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		Expect(etcdServiceService.Spec.ClusterIP).NotTo(BeEmpty())
		Expect(etcdServiceService.Spec.Selector).To(Equal(map[string]string{"app": "virtual-garden", "component": "etcd", "role": role}))
		Expect(etcdServiceService.Spec.SessionAffinity).To(Equal(corev1.ServiceAffinityNone))
		Expect(etcdServiceService.Spec.Ports).To(HaveLen(2))
		Expect(etcdServiceService.Spec.Ports[0].Name).To(Equal("client"))
		Expect(etcdServiceService.Spec.Ports[0].Port).To(Equal(int32(2379)))
		Expect(etcdServiceService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(2379)))
		Expect(etcdServiceService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		Expect(etcdServiceService.Spec.Ports[0].NodePort).To(BeZero())
		Expect(etcdServiceService.Spec.Ports[1].Name).To(Equal("backup-client"))
		Expect(etcdServiceService.Spec.Ports[1].Port).To(Equal(int32(8080)))
		Expect(etcdServiceService.Spec.Ports[1].TargetPort).To(Equal(intstr.FromInt(8080)))
		Expect(etcdServiceService.Spec.Ports[1].Protocol).To(Equal(corev1.ProtocolTCP))
		Expect(etcdServiceService.Spec.Ports[1].NodePort).To(BeZero())

		By("Checking that the etcd bootstrap configmap was created as expected (" + role + ")")
		etcdConfigMap := &corev1.ConfigMap{}
		Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDConfigMapName(role), Namespace: imports.HostingCluster.Namespace}, etcdConfigMap)).To(Succeed())

		Expect(etcdConfigMap.Data).To(HaveKey(virtualgarden.ETCDConfigMapDataKeyBootstrapScript))
		Expect(etcdConfigMap.Data[virtualgarden.ETCDConfigMapDataKeyBootstrapScript]).NotTo(BeEmpty())
		Expect(etcdConfigMap.Data).To(HaveKey(virtualgarden.ETCDConfigMapDataKeyConfiguration))
		Expect(etcdConfigMap.Data[virtualgarden.ETCDConfigMapDataKeyConfiguration]).NotTo(BeEmpty())

		By("Checking that the etcd server secret was created as expected (" + role + ")")
		etcdSecretServer := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameServerCertificate(role), Namespace: imports.HostingCluster.Namespace}, etcdSecretServer)).To(Succeed())

		etcdServerCertificate, err := secretsutil.LoadCertificate(etcdSecretServer.Name, etcdSecretServer.Data[secretsutil.DataKeyPrivateKey], etcdSecretServer.Data[secretsutil.DataKeyCertificate])
		Expect(err).NotTo(HaveOccurred())
		Expect(etcdServerCertificate.Certificate.IsCA).To(BeFalse())
		Expect(etcdServerCertificate.Certificate.Subject.CommonName).To(Equal("virtual-garden:server:etcd:" + role))
		Expect(etcdServerCertificate.Certificate.DNSNames).To(Equal([]string{
			fmt.Sprintf("virtual-garden-etcd-%s-0", role),
			fmt.Sprintf("virtual-garden-etcd-%s-client.%s", role, imports.HostingCluster.Namespace),
			fmt.Sprintf("virtual-garden-etcd-%s-client.%s.svc", role, imports.HostingCluster.Namespace),
			fmt.Sprintf("virtual-garden-etcd-%s-client.%s.svc.cluster", role, imports.HostingCluster.Namespace),
			fmt.Sprintf("virtual-garden-etcd-%s-client.%s.svc.cluster.local", role, imports.HostingCluster.Namespace),
		}))
		Expect(etcdServerCertificate.Certificate.Issuer.CommonName).To(Equal(etcdCACertificate.Certificate.Subject.CommonName))
		Expect(etcdServerCertificate.Certificate.KeyUsage).To(Equal(x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment))
		Expect(etcdServerCertificate.Certificate.ExtKeyUsage).To(Equal([]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}))

		By("Checking that the etcd statefulset was created as expected (" + role + ")")
		storageProviderName, _, environment := backupProvider.ComputeETCDBackupConfiguration(virtualgarden.ETCDVolumeMountPathBackupSecret)
		sts := &appsv1.StatefulSet{}
		Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStatefulSetName(role), Namespace: imports.HostingCluster.Namespace}, sts)).To(Succeed())

		Expect(sts.Labels).To(HaveKeyWithValue("app", "virtual-garden"))
		Expect(sts.Labels).To(HaveKeyWithValue("component", "etcd"))
		Expect(sts.Labels).To(HaveKeyWithValue("role", role))
		Expect(sts.Spec.Replicas).To(PointTo(Equal(int32(1))))
		Expect(sts.Spec.Selector.MatchLabels).To(Equal(map[string]string{"app": "virtual-garden", "component": "etcd", "role": role}))
		Expect(sts.Spec.ServiceName).To(Equal(virtualgarden.ETCDServiceName(role)))
		Expect(sts.Spec.UpdateStrategy.Type).To(Equal(appsv1.RollingUpdateStatefulSetStrategyType))
		Expect(sts.Spec.Template.Annotations).To(HaveKey("checksum/configmap-etcd-bootstrap-config"))
		Expect(sts.Spec.Template.Annotations).To(HaveKey("checksum/secret-etcd-ca"))
		Expect(sts.Spec.Template.Annotations).To(HaveKey("checksum/secret-etcd-server"))
		Expect(sts.Spec.Template.Annotations).To(HaveKey("checksum/secret-etcd-client"))
		Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("app", "virtual-garden"))
		Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("component", "etcd"))
		Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("role", role))
		Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(2))
		if role == virtualgarden.ETCDRoleMain && helper.ETCDBackupEnabled(imports.VirtualGarden.ETCD) {
			Expect(sts.Spec.Template.Annotations).To(HaveKey("checksum/secret-etcd-backup"))
			Expect(sts.Spec.Template.Spec.Containers[1].Env).To(ConsistOf(append([]corev1.EnvVar{{
				Name:  "STORAGE_CONTAINER",
				Value: imports.VirtualGarden.ETCD.Backup.BucketName,
			}}, environment...)))
			Expect(sts.Spec.Template.Spec.Containers[1].Command).To(ContainElements(
				"--schedule=0 */24 * * *",
				"--defragmentation-schedule=0 1 * * *",
				"--storage-provider="+storageProviderName,
				"--store-prefix="+sts.Name,
				"--delta-snapshot-period=5m",
				"--delta-snapshot-memory-limit=104857600",
				"--embedded-etcd-quota-bytes=8589934592",
			))
		}
		Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
		Expect(sts.Spec.VolumeClaimTemplates[0].Name).To(Equal(virtualgarden.ETCDDataVolumeName(role)))
		Expect(sts.Spec.VolumeClaimTemplates[0].Spec.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}))
		if role == virtualgarden.ETCDRoleMain {
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName).To(PointTo(Equal(virtualgarden.ETCDStorageClassName(imports.VirtualGarden.ETCD))))
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests).To(HaveKey(corev1.ResourceStorage))
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("25Gi")))
		} else {
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName).To(BeNil())
			Expect(sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("10Gi")))
		}

		Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
			if err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStatefulSetName(role), Namespace: imports.HostingCluster.Namespace}, sts); err != nil {
				return false, err
			}
			return sts.Generation == sts.Status.ObservedGeneration && sts.Status.ReadyReplicas == 1, nil
		}, timeoutCtx.Done())).To(Succeed())

		if helper.ETCDHVPAEnabled(imports.VirtualGarden.ETCD) {
			By("Checking that the etcd HVPA was created as expected (" + role + ")")
			etcdHVPA := &hvpav1alpha1.Hvpa{}
			Expect(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDHVPAName(role), Namespace: imports.HostingCluster.Namespace}, etcdHVPA)).To(Or(Succeed(), MatchError(&meta.NoResourceMatchError{}), MatchError(&meta.NoKindMatchError{})))
		}
	}
}

func verifyDeletion(ctx context.Context, c client.Client, imports *api.Imports) {
	By("Checking that the kube-apiserver load balancer service was deleted successfully")
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
		if err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.KubeAPIServerServiceName, Namespace: imports.HostingCluster.Namespace}, &corev1.Service{}); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	}, timeoutCtx.Done())).To(Succeed())

	timeoutCtx, cancel = context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	for _, role := range []string{virtualgarden.ETCDRoleMain, virtualgarden.ETCDRoleEvents} {
		if helper.ETCDHVPAEnabled(imports.VirtualGarden.ETCD) {
			By("Checking that the etcd HVPA was deleted successfully (" + role + ")")
			err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDHVPAName(role), Namespace: imports.HostingCluster.Namespace}, &hvpav1alpha1.Hvpa{})
			Expect(err).To(HaveOccurred())
			if !apierrors.IsNotFound(err) {
				Expect(err).To(Or(MatchError(&meta.NoResourceMatchError{}), MatchError(&meta.NoKindMatchError{})))
			}
		}

		By("Checking that the etcd statefulset was deleted successfully (" + role + ")")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStatefulSetName(role), Namespace: imports.HostingCluster.Namespace}, &appsv1.StatefulSet{}))).To(BeTrue())

		if handleETCDPersistentVolumes {
			By("Checking that the etcd persistent volume and persistent volume claims were deleted successfully (" + role + ")")
			pvcName := fmt.Sprintf("%s-%s-0", virtualgarden.ETCDDataVolumeName(role), virtualgarden.ETCDStatefulSetName(role))

			Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
				if err := c.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: imports.HostingCluster.Namespace}, &corev1.PersistentVolumeClaim{}); err != nil {
					if apierrors.IsNotFound(err) {
						return true, nil
					}
					return false, err
				}
				return false, nil
			}, timeoutCtx.Done())).To(Succeed())

			Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
				pvList := &corev1.PersistentVolumeList{}
				if err := c.List(ctx, pvList); err != nil {
					return false, err
				}

				for _, pv := range pvList.Items {
					if pv.Spec.ClaimRef.Namespace == imports.HostingCluster.Namespace && pv.Spec.ClaimRef.Name == pvcName {
						return false, nil
					}
				}

				return true, nil
			}, timeoutCtx.Done())).To(Succeed())
		}

		By("Checking that the etcd pod was deleted successfully (" + role + ")")
		Expect(wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
			if err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStatefulSetName(role) + "-0", Namespace: imports.HostingCluster.Namespace}, &corev1.Pod{}); err != nil {
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}
			return false, nil
		}, timeoutCtx.Done())).To(Succeed())

		By("Checking that the etcd bootstrap configmap was deleted successfully (" + role + ")")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDConfigMapName(role), Namespace: imports.HostingCluster.Namespace}, &corev1.ConfigMap{}))).To(BeTrue())

		By("Checking that the etcd service was deleted successfully (" + role + ")")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDServiceName(role), Namespace: imports.HostingCluster.Namespace}, &corev1.Service{}))).To(BeTrue())

		By("Checking that the etcd server certificate was deleted successfully (" + role + ")")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameServerCertificate(role), Namespace: imports.HostingCluster.Namespace}, &corev1.Secret{}))).To(BeTrue())
	}

	By("Checking that the etcd client certificate was deleted successfully")
	Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameClientCertificate, Namespace: imports.HostingCluster.Namespace}, &corev1.Secret{}))).To(BeTrue())

	By("Checking that the etcd CA certificate was deleted successfully")
	Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameCACertificate, Namespace: imports.HostingCluster.Namespace}, &corev1.Secret{}))).To(BeTrue())

	By("Checking that the etcd storage class was deleted successfully")
	statefulSetList := &appsv1.StatefulSetList{}
	Expect(c.List(ctx, statefulSetList)).To(Succeed())
	if err := c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDStorageClassName(imports.VirtualGarden.ETCD)}, &storagev1.StorageClass{}); len(statefulSetList.Items) == 0 {
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	} else {
		Expect(err).To(BeNil())
	}

	if helper.ETCDBackupEnabled(imports.VirtualGarden.ETCD) {
		By("Checking that the etcd backup secret was deleted successfully)")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: virtualgarden.ETCDSecretNameBackup, Namespace: imports.HostingCluster.Namespace}, &corev1.Secret{}))).To(BeTrue())

		By("Checking that the blob storage bucket was deleted successfully")
		backupProvider, err := provider.NewBackupProvider(imports.VirtualGarden.ETCD.Backup.InfrastructureProvider, imports.Credentials, imports.VirtualGarden.ETCD.Backup.CredentialsRef)
		Expect(err).NotTo(HaveOccurred())

		bucketExists, err := backupProvider.BucketExists(ctx, imports.VirtualGarden.ETCD.Backup.BucketName)
		Expect(err).NotTo(HaveOccurred())
		Expect(bucketExists).To(BeFalse())
	}
}

// See https://github.com/onsi/ginkgo/issues/285#issuecomment-290575636
func setCommandLineArguments() []string {
	origArgs := os.Args[:]
	if handleETCDPersistentVolumes {
		os.Args = append(os.Args[:1], "--handle-etcd-persistent-volumes")
	} else {
		os.Args = os.Args[:1]
	}
	return origArgs[:]
}