/*
Copyright 2022.

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

package controllers

import (
	"context"
	"strconv"

	"github.com/chinmayrelkar/aws-nlb-controller/aws"
	"github.com/chinmayrelkar/aws-nlb-controller/store"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	serviceAnnotation     = "github.com/chinmayrelkar/service"
	nlbAnnotationNLBHost  = "service-nlb-host"
	nlbAnnotationNLBName  = "service-nlb-name"
	nlbAnnotationPort     = "service-nlb-port"
	nlbAnnotationListener = "service-nlb-listener"
	nlbAnnotationTarget   = "service-nlb-target"
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Store     store.Store
	AwsClient aws.Client
}

// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	serviceName := req.NamespacedName.String()
	logger := log.FromContext(ctx)
	logger = logger.WithValues("svc", serviceName)

	// got a svc event
	// if svc exists then it was created/updated or controller has just started
	// if svc doesn't exist then delete listener, target group, release port for nlb in memory

	var svc corev1.Service
	err := r.Get(ctx, req.NamespacedName, &svc)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("svc does not exist")
		logger.Info("Deleting listener and target groups")
		allocation := r.Store.GetAllocationForSVC(ctx, serviceName)
		if allocation == nil {
			logger.Info("no allocation found")
			return ctrl.Result{}, nil
		}

		err := r.AwsClient.DeleteListenerAndTargetArn(allocation.ListenerArn, allocation.TargetArn)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}

		logger.Info("Releasing Port on NLB in memory")
		r.Store.ReleaseNLBAndPortForService(ctx, serviceName, allocation.NLB, allocation.Port)
		return ctrl.Result{}, nil
	}

	if err != nil {
		// failed to fetch service. can be problem with API service or network issue. Report as error and Requeue
		logger.Error(err, "unable to fetch service")
		return ctrl.Result{Requeue: true}, err
	}

	// svc found
	svcIsOfTypeNodePort := svc.Spec.Type == corev1.ServiceTypeNodePort
	if !svcIsOfTypeNodePort {
		logger.Info("svc not of type NodePort. Skipping")
		return ctrl.Result{}, nil
	}

	// check annotation
	isNodePortService := svc.Annotations[serviceAnnotation] == "true"
	isNLBPortAllocated := svc.Annotations[nlbAnnotationNLBName] != ""

	if !isNodePortService {
		logger.Info("svc not a NodePort service. Skipping")
	}

	// svc is a Node Port svc
	if isNLBPortAllocated {
		logger.Info("NodePort already allocated.")
		svcAllocatedListenerArn := svc.Annotations[nlbAnnotationListener]
		svcAllocatedTargetArn := svc.Annotations[nlbAnnotationTarget]
		svcAllocatedNLB := svc.Annotations[nlbAnnotationNLBName]
		svcAllocatedNodePort := int(svc.Spec.Ports[0].NodePort)

		svcAllocatedPort, err := strconv.Atoi(svc.Annotations[nlbAnnotationPort])
		if err != nil {
			logger.Error(err, "malformed port in svc labels. reallocating")
		} else {
			err := r.checkAllocationValidity(
				ctx,
				serviceName,
				svcAllocatedListenerArn,
				svcAllocatedTargetArn,
				svcAllocatedNLB,
				svcAllocatedPort,
				svcAllocatedNodePort,
			)
			if err != nil {
				logger.Error(err, "reallocating")
			} else {
				logger.Info("Validation successful. Skipping")
				return ctrl.Result{}, nil
			}
		}
	}

	// If the label should be set but is not, set it.
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}

	nlb, nlbPort, err := r.Store.GetVacantNLBAndPortForService(ctx, serviceName)
	if err != nil {
		logger.Error(err, "unable to get vacant nlb and port")
		return ctrl.Result{Requeue: true}, err
	}

	nodePort := int(svc.Spec.Ports[0].NodePort)
	logger = logger.WithValues("nlb", nlb, "nlbPort", nlbPort, "nodePort", nodePort)

	listenerArn, targetArn, err := r.AwsClient.CreateNLBListenerForPort(
		nlb,
		nlbPort,
		nodePort,
		req.NamespacedName.String(),
	)
	if err != nil {
		logger.Error(err, "unable to create listener nlb ")
		r.Store.ReleaseNLBAndPortForService(ctx, serviceName, nlb, nlbPort)
		return ctrl.Result{Requeue: true}, err
	}

	err = r.Store.AssignNLBAndPortToServiceInNamespace(
		ctx,
		nlb,
		nlbPort,
		serviceName,
		listenerArn,
		targetArn,
	)
	if err != nil {
		logger.Error(err, "unable to save listener nlb allocation")
		r.Store.ReleaseNLBAndPortForService(ctx, serviceName, nlb, nlbPort)
		err2 := r.AwsClient.DeleteListenerAndTargetArn(listenerArn, targetArn)
		if err2 != nil {
			logger.Error(err2, "SEV0: failed to delete listener for a failed allocation")
			return ctrl.Result{Requeue: false}, err2
		}
		return ctrl.Result{Requeue: true}, err
	}

	svc.Annotations[nlbAnnotationNLBName] = nlb
	svc.Annotations[nlbAnnotationNLBHost] = r.Store.GetNLBHost(nlb)
	svc.Annotations[nlbAnnotationPort] = strconv.Itoa(nlbPort)
	svc.Annotations[nlbAnnotationListener] = listenerArn
	svc.Annotations[nlbAnnotationTarget] = targetArn

	if err := r.Update(ctx, &svc); err != nil {
		logger.Error(err, "unable to update svc")

		r.Store.ReleaseNLBAndPortForService(ctx, req.NamespacedName.String(), "", 0)
		err2 := r.AwsClient.DeleteListenerAndTargetArn(listenerArn, targetArn)
		if err2 != nil {
			logger.Error(err2, "SEV0: failed to delete listener for a failed svc object update")
			return ctrl.Result{Requeue: false}, err2
		}

		if apierrors.IsNotFound(err) {
			return ctrl.Result{Requeue: false}, nil
		}
		return ctrl.Result{Requeue: true}, nil
	}
	logger.Info("Load balancer assigned and label added")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}

func (r *ServiceReconciler) checkAllocationValidity(
	ctx context.Context,
	serviceName string,
	svcAllocatedListenerArn string,
	svcAllocatedTargetArn string,
	svcAllocatedNLB string,
	svcAllocatedPort int,
	svcAllocatedNodePort int,
) error {
	err := r.AwsClient.CheckListener(
		ctx,
		svcAllocatedListenerArn,
		svcAllocatedTargetArn,
		svcAllocatedNLB,
		svcAllocatedPort,
		svcAllocatedNodePort,
	)
	if err != nil {
		return err
	}
	err = r.Store.AssignNLBAndPortToServiceInNamespace(
		ctx,
		svcAllocatedNLB,
		svcAllocatedPort,
		serviceName,
		svcAllocatedListenerArn,
		svcAllocatedTargetArn,
	)
	if err != nil {
		return err
	}
	return nil
}
