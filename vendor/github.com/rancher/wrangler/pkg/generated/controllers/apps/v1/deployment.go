/*
Copyright The Kubernetes Authors.

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

// Code generated by main. DO NOT EDIT.

package v1

import (
	"context"
	"time"

	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/generic"
	"github.com/rancher/wrangler/pkg/kv"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// DeploymentController interface for managing Deployment resources.
type DeploymentController interface {
	generic.ControllerInterface[*v1.Deployment, *v1.DeploymentList]
}

// DeploymentClient interface for managing Deployment resources in Kubernetes.
type DeploymentClient interface {
	generic.ClientInterface[*v1.Deployment, *v1.DeploymentList]
}

// DeploymentCache interface for retrieving Deployment resources in memory.
type DeploymentCache interface {
	generic.CacheInterface[*v1.Deployment]
}

type DeploymentStatusHandler func(obj *v1.Deployment, status v1.DeploymentStatus) (v1.DeploymentStatus, error)

type DeploymentGeneratingHandler func(obj *v1.Deployment, status v1.DeploymentStatus) ([]runtime.Object, v1.DeploymentStatus, error)

func RegisterDeploymentStatusHandler(ctx context.Context, controller DeploymentController, condition condition.Cond, name string, handler DeploymentStatusHandler) {
	statusHandler := &deploymentStatusHandler{
		client:    controller,
		condition: condition,
		handler:   handler,
	}
	controller.AddGenericHandler(ctx, name, generic.FromObjectHandlerToHandler(statusHandler.sync))
}

func RegisterDeploymentGeneratingHandler(ctx context.Context, controller DeploymentController, apply apply.Apply,
	condition condition.Cond, name string, handler DeploymentGeneratingHandler, opts *generic.GeneratingHandlerOptions) {
	statusHandler := &deploymentGeneratingHandler{
		DeploymentGeneratingHandler: handler,
		apply:                       apply,
		name:                        name,
		gvk:                         controller.GroupVersionKind(),
	}
	if opts != nil {
		statusHandler.opts = *opts
	}
	controller.OnChange(ctx, name, statusHandler.Remove)
	RegisterDeploymentStatusHandler(ctx, controller, condition, name, statusHandler.Handle)
}

type deploymentStatusHandler struct {
	client    DeploymentClient
	condition condition.Cond
	handler   DeploymentStatusHandler
}

func (a *deploymentStatusHandler) sync(key string, obj *v1.Deployment) (*v1.Deployment, error) {
	if obj == nil {
		return obj, nil
	}

	origStatus := obj.Status.DeepCopy()
	obj = obj.DeepCopy()
	newStatus, err := a.handler(obj, obj.Status)
	if err != nil {
		// Revert to old status on error
		newStatus = *origStatus.DeepCopy()
	}

	if a.condition != "" {
		if errors.IsConflict(err) {
			a.condition.SetError(&newStatus, "", nil)
		} else {
			a.condition.SetError(&newStatus, "", err)
		}
	}
	if !equality.Semantic.DeepEqual(origStatus, &newStatus) {
		if a.condition != "" {
			// Since status has changed, update the lastUpdatedTime
			a.condition.LastUpdated(&newStatus, time.Now().UTC().Format(time.RFC3339))
		}

		var newErr error
		obj.Status = newStatus
		newObj, newErr := a.client.UpdateStatus(obj)
		if err == nil {
			err = newErr
		}
		if newErr == nil {
			obj = newObj
		}
	}
	return obj, err
}

type deploymentGeneratingHandler struct {
	DeploymentGeneratingHandler
	apply apply.Apply
	opts  generic.GeneratingHandlerOptions
	gvk   schema.GroupVersionKind
	name  string
}

func (a *deploymentGeneratingHandler) Remove(key string, obj *v1.Deployment) (*v1.Deployment, error) {
	if obj != nil {
		return obj, nil
	}

	obj = &v1.Deployment{}
	obj.Namespace, obj.Name = kv.RSplit(key, "/")
	obj.SetGroupVersionKind(a.gvk)

	return nil, generic.ConfigureApplyForObject(a.apply, obj, &a.opts).
		WithOwner(obj).
		WithSetID(a.name).
		ApplyObjects()
}

func (a *deploymentGeneratingHandler) Handle(obj *v1.Deployment, status v1.DeploymentStatus) (v1.DeploymentStatus, error) {
	if !obj.DeletionTimestamp.IsZero() {
		return status, nil
	}

	objs, newStatus, err := a.DeploymentGeneratingHandler(obj, status)
	if err != nil {
		return newStatus, err
	}

	return newStatus, generic.ConfigureApplyForObject(a.apply, obj, &a.opts).
		WithOwner(obj).
		WithSetID(a.name).
		ApplyObjects(objs...)
}
