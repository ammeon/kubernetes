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
	"errors"

	"github.com/rackspace/gophercloud/openstack/compute/v2/servers"
	"github.com/rackspace/gophercloud/openstack/networking/v2/extensions/layer3/routers"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

// Routes returns an implementation of Routes for OpenStack.
func (os *OpenStack) Routes() (cloudprovider.Routes, bool) {
	err := os.Network()
	if err != nil {
		return nil, false
	}
	err = os.Compute()
	if err != nil {
		return nil, false
	}
	return os, true
}

// ListRoutes is an implementation of Routes.ListRoutes.
func (os *OpenStack) ListRoutes(clusterName string) ([]*cloudprovider.Route, error) {
	glog.V(4).Info("openstack.ListRoutes() called")

	var routes []*cloudprovider.Route
	router, err := routers.Get(os.network, os.routeOpts.RouterId).Extract()
	if err != nil {
		return nil, err
	}

	for _, r := range router.Routes {
		server, err := getServerByAddress(os.compute, r.NextHop)
		if err != nil {
			return nil, err
		}
		target, err := os.getKubernetesMetaData(server.ID)
		if err != nil {
			// Yes this is wrong. But this covers the case ListRoutes is being called before the appropriate MetaData has been created.
			// By purposefully mismatching the name CreateRoute should be rerun and the correct Metadata be set
			target = server.Name
		}
		route := cloudprovider.Route{
			Name:            r.DestinationCIDR,
			TargetInstance:  cloudprovider.Instance{Name: target},
			DestinationCIDR: r.DestinationCIDR,
		}
		routes = append(routes, &route)
	}
	return routes, err
}

// CreateRoute is an implementation of Routes.CreateRoute.
// route.Name will be ignored, although the cloud-provider may use nameHint
// to create a more user-meaningful name.
func (os *OpenStack) CreateRoute(clusterName string, nameHint string, route *cloudprovider.Route) error {
	glog.V(4).Info("openstack.CreateRoute() called")

	if route.TargetInstance.ID == "" {
		return errors.New("Route must provide ID for OpenStack cloud provider")
	}

	server, err := servers.Get(os.compute, getInstanceIDFromProviderID(route.TargetInstance.ID)).Extract()
	if err != nil {
		return err
	}

	err = os.createKubernetesMetaData(server.ID, route.TargetInstance.Name)
	if err != nil {
		return err
	}

	router, err := routers.Get(os.network, os.routeOpts.RouterId).Extract()
	if err != nil {
		return err
	}
	addrs, err := getAddresses(server)
	addr := addrs[0].Address

	err = os.setAllowedAddressPair(server, route.DestinationCIDR)
	if err != nil {
		return err
	}

	routes := router.Routes
	routes = append(routes, routers.Route{DestinationCIDR: route.DestinationCIDR, NextHop: addr})
	opts := routers.UpdateOpts{Routes: routes}

	_, err = routers.Update(os.network, router.ID, opts).Extract()
	if err != nil {
		return err
	}
	glog.V(4).Infof("Route Created: %s %s %s", clusterName, nameHint, routes)
	return nil
}

// Delete the specified managed route
// Route should be as returned by ListRoutes
func (os *OpenStack) DeleteRoute(clusterName string, route *cloudprovider.Route) error {
	glog.V(4).Info("openstack.DeleteRoute() called")

	router, err := routers.Get(os.network, os.routeOpts.RouterId).Extract()
	if err != nil {
		return err
	}

	index := -1
	for i, r := range router.Routes {
		if r.DestinationCIDR == route.DestinationCIDR {
			index = i
			break
		}
	}

	routes := append(router.Routes[:index], router.Routes[index+1:]...)
	opts := routers.UpdateOpts{Routes: routes}

	_, err = routers.Update(os.network, router.ID, opts).Extract()
	if err != nil {
		return err
	}
	glog.V(4).Infof("Route deleted: %s %s", clusterName, route)
	return nil
}
