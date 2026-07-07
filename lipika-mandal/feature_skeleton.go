package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type TypeMeta struct {
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

type DpuOperatorConfigSpec struct {
	LogLevel int `json:"logLevel,omitempty"`
}

type DpuOperatorConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type DpuOperatorConfig struct {
	TypeMeta          `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DpuOperatorConfigSpec   `json:"spec,omitempty"`
	Status DpuOperatorConfigStatus `json:"status,omitempty"`
}

type componentError struct {
	component string
	err       error
}

type NvidiaDpfTranslationEngine struct {
	AdapterNamespace string
}

// GenerateValidDpfYaml builds a fully valid, machine-readable Kubernetes YAML block with a corrected group layout
func (e *NvidiaDpfTranslationEngine) GenerateValidDpfYaml(config DpuOperatorConfig, bfbUrl string, chartName string, flavour string, fsMode string) string {
	return fmt.Sprintf(`apiVersion: ://nvidia.com
kind: DPUService
metadata:
  name: nvidia-translated-spec
  namespace: %s
spec:
  clusterFlavour: %s
  fileSystemMode: %s
  targetBfbUrl: %s
  helmChart: %s
  logLevel: %d`, e.AdapterNamespace, flavour, fsMode, bfbUrl, chartName, config.Spec.LogLevel)
}

// ReconcileNvidiaNode manages condition updates matching the controller-runtime reconcile engine specifications
func (e *NvidiaDpfTranslationEngine) ReconcileNvidiaNode(ctx context.Context, config *DpuOperatorConfig, bfbUrl string, chartName string, flavour string, fsMode string, secureBootMismatched bool) (reconcile.Result, []componentError) {
	fmt.Printf("[OPI Engine] Syncing DpuOperatorConfig Instance: %s via Controller-Runtime Stream\n", config.Name)
	var reconcileErrors []componentError

	if secureBootMismatched {
		err := reconcile.TerminalError(errors.New("terminal hardware security mismatch: manual intervention required"))
		reconcileErrors = append(reconcileErrors, componentError{component: "NvidiaSecureBoot", err: err})
		return reconcile.Result{}, reconcileErrors
	}

	if bfbUrl == "" || chartName == "" {
		err := errors.New("target BlueField Bundle URL and Helm Chart parameters cannot be empty")
		reconcileErrors = append(reconcileErrors, componentError{component: "NvidiaTranslation", err: err})
		return reconcile.Result{}, reconcileErrors
	}

 	dpfPayload := e.GenerateValidDpfYaml(*config, bfbUrl, chartName, flavour, fsMode)
	fmt.Printf("[NVIDIA Adapter Engine] Emitted Discovered Environment Workspace Payload:\n%s\n", dpfPayload)
	
	return reconcile.Result{}, nil
}

// updateStatus leverages the official meta.SetStatusCondition package to execute upsert/mutation operations safely
func (e *NvidiaDpfTranslationEngine) updateStatus(config *DpuOperatorConfig, reconcileErrors []componentError) {
	if len(reconcileErrors) > 0 {
		firstError := reconcileErrors[0]
		reasonStr := fmt.Sprintf("%sError", firstError.component)
		
		meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             reasonStr,
			Message:            firstError.err.Error(),
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
		fmt.Printf("[OPI Controller Status Logger] Set Status Condition Reason: %s\n", reasonStr)
		return
	}

	meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ComponentsReady",
		Message:            "All NVIDIA DOCA and OPI components reconciled successfully.",
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
	fmt.Println("[OPI Controller Status Logger] Set Status Condition Reason: ComponentsReady")
}

func main() {
	engine := &NvidiaDpfTranslationEngine{AdapterNamespace: "openshift-dpu-operator"}
	
	mockClusterConfig := &DpuOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "dpu-operator-config"},
		Spec:       DpuOperatorConfigSpec{LogLevel: 0},
		Status:     DpuOperatorConfigStatus{Conditions: []metav1.Condition{}},
	}
	
	targetBfb := "https://mellanox.com"
	
	fmt.Println("--- Running Successful Environmental Discovery Stream ---")
	res, errs := engine.ReconcileNvidiaNode(context.Background(), mockClusterConfig, targetBfb, "doca-hbn", "OpenShift", "host-trusted", false)
	engine.updateStatus(mockClusterConfig, errs)
	fmt.Printf("[Success Loop Result] Zero Value: %t, Total Errors Cached: %d\n\n", res == reconcile.Result{}, len(errs))
	
	fmt.Println("--- Running SecureBoot Component Error Trapping Stream ---")
	mockClusterConfigError := &DpuOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "dpu-operator-config-err"},
		Spec:       DpuOperatorConfigSpec{LogLevel: 0},
		Status:     DpuOperatorConfigStatus{Conditions: []metav1.Condition{}},
	}
	resErr, errsErr := engine.ReconcileNvidiaNode(context.Background(), mockClusterConfigError, targetBfb, "doca-hbn", "OpenShift", "host-trusted", true)
	engine.updateStatus(mockClusterConfigError, errsErr)
	fmt.Printf("[Terminal Loop Result] Zero Value: %t, Total Errors Cached: %d\n", resErr == reconcile.Result{}, len(errsErr))
}
