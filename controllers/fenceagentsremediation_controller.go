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
	"errors"
	"fmt"

	"github.com/go-logr/logr"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	errorMissingParams     = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
	errorMissingNodeParams = "node parameter is required, and cannot be empty"
	SuccessFAResponse      = "Success: Rebooted"
)

// FenceAgentsRemediationReconciler reconciles a FenceAgentsRemediation object
type FenceAgentsRemediationReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Executor cli.Executer
}

// SetupWithManager sets up the controller with the Manager.
func (r *FenceAgentsRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.FenceAgentsRemediation{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;delete;deletecollection
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;delete
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the FenceAgentsRemediation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *FenceAgentsRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log.Info("Begin FenceAgentsRemediation Reconcile")
	defer r.Log.Info("Finish FenceAgentsRemediation Reconcile")
	emptyResult := ctrl.Result{}

	// Fetch the FenceAgentsRemediation instance
	far := &v1alpha1.FenceAgentsRemediation{}
	if err := r.Get(ctx, req.NamespacedName, far); err != nil {
		if apiErrors.IsNotFound(err) {
			// FenceAgentsRemediation CR was not found, and it could have been deleted after reconcile request.
			// Return and don't requeue
			r.Log.Info("FenceAgentsRemediation CR was not found", "CR Name", req.Name, "CR Namespace", req.Namespace)
			return emptyResult, nil
		}
		r.Log.Error(err, "Failed to get FenceAgentsRemediation CR")
		return emptyResult, err
	}
	// Validate FAR CR name to match a nodeName from the cluster
	r.Log.Info("Check FAR CR's name")
	valid, err := utils.IsNodeNameValid(r.Client, req.Name)
	if err != nil {
		r.Log.Error(err, "Unexpected error when validating CR's name with nodes' names", "CR's Name", req.Name)
		return emptyResult, err
	}
	if !valid {
		r.Log.Error(err, "Didn't find a node matching the CR's name", "CR's Name", req.Name)
		return emptyResult, nil
	}

	// Add finalizer when the CR is created
	if !controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && far.ObjectMeta.DeletionTimestamp.IsZero() {
		controllerutil.AddFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to add finalizer to the CR - %w", err)
		}
	} else if controllerutil.ContainsFinalizer(far, v1alpha1.FARFinalizer) && !far.ObjectMeta.DeletionTimestamp.IsZero() {
		// Delete CR only when a finalizer and DeletionTimestamp are set
		r.Log.Info("CR's deletion timestamp is not zero, and FAR finalizer exists", "CR Name", req.Name)
		// remove node's taints
		if err := utils.RemoveTaint(r.Client, far.Name); err != nil && !apiErrors.IsNotFound(err) {
			return emptyResult, err
		}
		// remove finalizer
		controllerutil.RemoveFinalizer(far, v1alpha1.FARFinalizer)
		if err := r.Client.Update(context.Background(), far); err != nil {
			return emptyResult, fmt.Errorf("failed to remove finalizer from CR - %w", err)
		}
		r.Log.Info("Finalizer was removed", "CR Name", req.Name)
		return emptyResult, nil
	}
	// find node by name
	node, err := utils.GetNodeWithName(r, req.Name)
	if err != nil {
		return emptyResult, err
	}
	// check if taint doesn't exist
	taint := utils.CreateFARNoExecuteTaint()
	hasNewTaint := utils.TaintExists(node.Spec.Taints, &taint)

	// Add medik8s remediation taint
	r.Log.Info("Add Medik8s remediation taint", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
	if err := utils.AppendTaint(r.Client, far.Name); err != nil {
		return emptyResult, err
	}
	if hasNewTaint {
		r.Log.Info("Don't build and exec FA when the remediation taint was not present in the beginning", "CR's Name", req.Name)
	} else {
		// Fetch the FAR's pod
		r.Log.Info("Fetch FAR's pod")
		pod, err := utils.GetFenceAgentsRemediationPod(r.Client)
		if err != nil {
			r.Log.Error(err, "Can't find FAR's pod by it's label", "CR's Name", req.Name)
			return emptyResult, err
		}
		//TODO: Check that FA is excutable? run cli.IsExecuteable

		// Build FA parameters
		r.Log.Info("Combine fence agent parameters", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
		faParams, err := buildFenceAgentParams(far)
		if err != nil {
			r.Log.Error(err, "Invalid sharedParameters/nodeParameters from CR", "CR's Name", req.Name)
			return emptyResult, err
		}

		cmd := append([]string{far.Spec.Agent}, faParams...)
		// The Fence Agent is excutable and the parameters structure are valid, but we don't check their values
		r.Log.Info("Execute the fence agent", "Fence Agent", far.Spec.Agent, "Node Name", req.Name)
		outputRes, outputErr, err := r.Executor.Execute(pod, cmd)
		if err != nil {
			// response was a failure message
			r.Log.Error(err, "Fence Agent response was a failure", "CR's Name", req.Name)
			return emptyResult, err
		}
		if outputErr != "" || outputRes == "" {
			// response wasn't failure or sucesss message
			err := fmt.Errorf("unknown fence agent response - expecting `%s` response, but we received `%s`", SuccessFAResponse, outputRes)
			r.Log.Error(err, "Fence Agent response wasn't a success message", "CR's Name", req.Name)
			return emptyResult, err
		}
	}
	return emptyResult, nil
}

// buildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParamter name then return an error
func buildFenceAgentParams(far *v1alpha1.FenceAgentsRemediation) ([]string, error) {
	if far.Spec.NodeParameters == nil || far.Spec.SharedParameters == nil {
		return nil, errors.New(errorMissingParams)
	}
	var fenceAgentParams []string
	for paramName, paramVal := range far.Spec.SharedParameters {
		fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, paramVal)
	}

	nodeName := v1alpha1.NodeName(far.Name)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[nodeName]; isFound {
			fenceAgentParams = appendParamToSlice(fenceAgentParams, paramName, nodeVal)
		} else {
			err := errors.New(errorMissingNodeParams)
			return nil, err
		}
	}
	return fenceAgentParams, nil
}

// appendParamToSlice appends parameters in a key-value manner, when value can be empty
func appendParamToSlice(fenceAgentParams []string, paramName v1alpha1.ParameterName, paramVal string) []string {
	if paramVal != "" {
		fenceAgentParams = append(fenceAgentParams, fmt.Sprintf("%s=%s", paramName, paramVal))
	} else {
		fenceAgentParams = append(fenceAgentParams, string(paramName))
	}
	return fenceAgentParams
}
