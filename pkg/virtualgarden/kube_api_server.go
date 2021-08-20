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
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeployKubeAPIServer deploys a kubernetes api server.
func (o *operation) DeployKubeAPIServer(ctx context.Context) error {
	o.log.Infof("Deploying the KubeAPIServer")

	checksums := make(map[string]string)

	loadBalancer, err := o.computeKubeAPIServerLoadBalancer(ctx)
	if err != nil {
		return err
	}

	err = o.deployKubeAPIServerCertificates(ctx, loadBalancer, checksums)
	if err != nil {
		return err
	}

	return nil
}

// DeleteKubeAPIServer deletes the kube-apiserver and all related resources.
func (o *operation) DeleteKubeAPIServer(ctx context.Context) error {
	if err := o.deleteKubeAPIServerCertificates(ctx); err != nil {
		return err
	}

	return nil
}

func (o *operation) computeKubeAPIServerLoadBalancer(ctx context.Context) (string, error) {
	var loadBalancer string

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	err := wait.PollImmediateUntil(2*time.Second, func() (done bool, err error) {
		service := emptyKubeAPIServerService(o.namespace)
		if err := o.client.Get(ctx, client.ObjectKeyFromObject(service), service); err != nil {
			return false, err
		}

		loadBalancer = o.infrastructureProvider.GetLoadBalancer(service)

		return loadBalancer != "", nil
	}, timeoutCtx.Done())

	if err != nil {
		return "", fmt.Errorf("Error reading loadbalancer IP: %w", err)
	}

	o.exports.VirtualGardenEndpoint = loadBalancer

	return loadBalancer, err
}

func kubeAPIServerLabels() map[string]string {
	return map[string]string{
		LabelKeyApp:       Prefix,
		LabelKeyComponent: "kube-apiserver",
	}
}
