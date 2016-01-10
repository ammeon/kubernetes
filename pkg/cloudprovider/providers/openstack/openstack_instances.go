/*
Copyright 2014 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"errors"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

// Instances returns an implementation of Instances for OpenStack.
func (os *OpenStack) Instances() (cloudprovider.Instances, bool) {
	glog.V(4).Info("openstack.Instances() called")

	glog.V(1).Info("Claiming to support Instances")

	return os, true
}

func (os *OpenStack) List(name_filter string) ([]string, error) {
	glog.V(4).Infof("openstack List(%v) called", name_filter)

	ret, err := findInstances(os.compute, name_filter)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// Implementation of Instances.CurrentNodeName
func (os *OpenStack) CurrentNodeName(hostname string) (string, error) {
	return hostname, nil
}

func (os *OpenStack) AddSSHKeyToAllInstances(user string, keyData []byte) error {
	return errors.New("unimplemented")
}

func (os *OpenStack) NodeAddresses(name string) ([]api.NodeAddress, error) {
	glog.V(4).Infof("NodeAddresses(%v) called", name)

	srv, err := getServerByName(os.compute, name)
	if err != nil {
		return nil, err
	}

	addrs := []api.NodeAddress{}

	for _, addr := range findAddrs(srv.Addresses, "fixed") {
		addrs = append(addrs, api.NodeAddress{
			Type:    api.NodeInternalIP,
			Address: addr,
		})
	}

	for _, addr := range findAddrs(srv.Addresses, "floating") {
		addrs = append(addrs, api.NodeAddress{
			Type:    api.NodeExternalIP,
			Address: addr,
		})
	}

	// AccessIPs are usually duplicates of "public" addresses.
	api.AddToNodeAddresses(&addrs,
		api.NodeAddress{
			Type:    api.NodeExternalIP,
			Address: srv.AccessIPv6,
		},
		api.NodeAddress{
			Type:    api.NodeExternalIP,
			Address: srv.AccessIPv4,
		},
	)

	glog.V(4).Infof("NodeAddresses(%v) => %v", name, addrs)
	return addrs, nil
}

// ExternalID returns the cloud provider ID of the specified instance (deprecated).
func (os *OpenStack) ExternalID(name string) (string, error) {
	srv, err := getServerByName(os.compute, name)
	if err != nil {
		return "", err
	}
	return srv.ID, nil
}

// InstanceID returns the cloud provider ID of the specified instance.
func (os *OpenStack) InstanceID(name string) (string, error) {
	srv, err := getServerByName(os.compute, name)
	if err != nil {
		return "", err
	}
	// In the future it is possible to also return an endpoint as:
	// <endpoint>/<instanceid>
	return "/" + srv.ID, nil
}
