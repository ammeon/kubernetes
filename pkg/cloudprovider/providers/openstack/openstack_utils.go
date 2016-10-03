/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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
	"fmt"
	"strings"

	"github.com/rackspace/gophercloud/openstack/compute/v2/servers"
	"github.com/rackspace/gophercloud/openstack/networking/v2/ports"
	"github.com/rackspace/gophercloud/pagination"
)

func getInstanceIDFromProviderID(providerID string) string {
	if ind := strings.LastIndex(providerID, "/"); ind >= 0 {
		return providerID[(ind + 1):]
	}
	return providerID
}

func (os *OpenStack) createKubernetesMetaData(serverID string, name string) error {
	updateOpts := servers.MetadataOpts{"KubernetesName": name}
	_, err := servers.UpdateMetadata(os.compute, serverID, updateOpts).Extract()
	if err != nil {
		return err
	}
	return nil
}

func (os *OpenStack) getKubernetesMetaData(serverID string) (string, error) {
	metadata, err := servers.Metadata(os.compute, serverID).Extract()
	if err != nil {
		return "", err
	}
	if val, ok := metadata["KubernetesName"]; ok {
		return val, nil
	}
	return "", fmt.Errorf("KubernetesName metadata does not exist on server: %s", serverID)
}

func (os *OpenStack) getServerFromMetadata(metadata string) (*servers.Server, error) {
	opts := servers.ListOpts{
		Status: "ACTIVE",
	}
	pager := servers.List(os.compute, opts)

	serverList := make([]servers.Server, 0, 1)

	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		s, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, server := range s {
			name, err := os.getKubernetesMetaData(server.ID)
			if err == nil && name == metadata {
				serverList = append(serverList, server)
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	if len(serverList) == 0 {
		return nil, ErrNotFound
	} else if len(serverList) > 1 {
		return nil, ErrMultipleResults
	}

	return &serverList[0], nil
}

func (os *OpenStack) setAllowedAddressPair(server *servers.Server, address string) error {
	var mac_addr string
	for _, netblob := range server.Addresses {
		list, ok := netblob.([]interface{})
		if !ok {
			continue
		}

		for _, item := range list {
			props, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			extIPType, ok := props["OS-EXT-IPS:type"]
			if ok && extIPType == "fixed" {
				mac_addr = props["OS-EXT-IPS-MAC:mac_addr"].(string)
			}
		}
	}
	listOpts := ports.ListOpts{MACAddress: mac_addr}
	var port ports.Port
	pager := ports.List(os.network, listOpts)

	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		portList, err := ports.ExtractPorts(page)
		if err != nil {
			return false, err
		}

		for _, s := range portList {
			port = s
			return true, nil
		}
		return true, nil
	})
	addressPairs := port.AllowedAddressPairs
	for _, pair := range addressPairs {
		if pair.IPAddress == address {
			return nil
		}
	}
	addressPairs = append(addressPairs, ports.AddressPair{IPAddress: address})
	updateOpts := ports.UpdateOpts{AllowedAddressPairs: addressPairs}

	_, err = ports.Update(os.network, port.ID, updateOpts).Extract()
	if err != nil {
		return err
	}
	return nil
}
