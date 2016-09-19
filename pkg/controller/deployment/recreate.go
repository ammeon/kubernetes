/*
Copyright 2016 The Kubernetes Authors.

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

package deployment

import (
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/controller"
)

// rolloutRecreate implements the logic for recreating a replica set.
func (dc *DeploymentController) rolloutRecreate(deployment *extensions.Deployment) error {
	// Don't create a new RS if not already existed, so that we avoid scaling up before scaling down
	newRS, oldRSs, err := dc.getAllReplicaSetsAndSyncRevision(deployment, false)
	if err != nil {
		return err
	}
	allRSs := append(oldRSs, newRS)

	// scale down old replica sets
	scaledDown, err := dc.scaleDownOldReplicaSetsForRecreate(controller.FilterActiveReplicaSets(oldRSs), deployment)
	if err != nil {
		return err
	}
	if scaledDown {
		// Update DeploymentStatus
		return dc.updateDeploymentStatus(allRSs, newRS, deployment)
	}

	// If we need to create a new RS, create it now
	// TODO: Create a new RS without re-listing all RSs.
	if newRS == nil {
		newRS, oldRSs, err = dc.getAllReplicaSetsAndSyncRevision(deployment, true)
		if err != nil {
			return err
		}
		allRSs = append(oldRSs, newRS)
	}

	// scale up new replica set
	scaledUp, err := dc.scaleUpNewReplicaSetForRecreate(newRS, deployment)
	if err != nil {
		return err
	}
	if scaledUp {
		// Update DeploymentStatus
		return dc.updateDeploymentStatus(allRSs, newRS, deployment)
	}

	dc.cleanupDeployment(oldRSs, deployment)

	// Sync deployment status
	return dc.syncDeploymentStatus(allRSs, newRS, deployment)
}

// scaleDownOldReplicaSetsForRecreate scales down old replica sets when deployment strategy is "Recreate"
func (dc *DeploymentController) scaleDownOldReplicaSetsForRecreate(oldRSs []*extensions.ReplicaSet, deployment *extensions.Deployment) (bool, error) {
	scaled := false
	for _, rs := range oldRSs {
		// Scaling not required.
		if rs.Spec.Replicas == 0 {
			continue
		}
		scaledRS, _, err := dc.scaleReplicaSetAndRecordEvent(rs, 0, deployment)
		if err != nil {
			return false, err
		}
		if scaledRS {
			scaled = true
		}
	}
	return scaled, nil
}

// scaleUpNewReplicaSetForRecreate scales up new replica set when deployment strategy is "Recreate"
func (dc *DeploymentController) scaleUpNewReplicaSetForRecreate(newRS *extensions.ReplicaSet, deployment *extensions.Deployment) (bool, error) {
	scaled, _, err := dc.scaleReplicaSetAndRecordEvent(newRS, deployment.Spec.Replicas, deployment)
	return scaled, err
}
