package controller

import (
    "context"
    "fmt"

    "github.com/go-logr/logr"
    configv1 "github.com/openshift/dpu-operator-nvidia-backend/api/v1"
    "k8s.io/apimachinery/pkg/api/meta"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

// NvidiaBackendAdapter defines the interface used by the reconciler to
// translate OPI DataProcessingUnit objects into NVIDIA DPF CRDs and sync state.
type NvidiaBackendAdapter interface {
    // SyncDPU ensures that DPF-side resources (e.g., DPUNode, DPUDevice, DPUSet)
    // exist and reflect the desired state for the given DataProcessingUnit.
    SyncDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error

    // CleanupDPU removes or detaches any DPF resources associated with the
    // given DataProcessingUnit during deletion.
    CleanupDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error
}

// DpfAdapter is a concrete implementation of NvidiaBackendAdapter that
// translates OPI DataProcessingUnit resources into NVIDIA DPF CRDs using
// unstructured objects (since DPF Go types are not imported in this skeleton).
type DpfAdapter struct {
    client.Client
    DpfNamespace string
}

// NewDpfAdapter constructs a new DpfAdapter targeting the given DPF namespace.
func NewDpfAdapter(c client.Client, dpfNamespace string) *DpfAdapter {
    return &DpfAdapter{
        Client:       c,
        DpfNamespace: dpfNamespace,
    }
}

// VendorDetector abstracts vendor detection logic so the reconciler does not
// rely on hardcoded product names. In production this would be backed by
// DpuDetectorManager from the OPI operator.
type VendorDetector interface {
    IsNvidiaDPU(dpu *configv1.DataProcessingUnit) bool
}

// DefaultNvidiaDetector is a minimal VendorDetector that recognizes known
// NVIDIA BlueField product names.
type DefaultNvidiaDetector struct{}

func (d *DefaultNvidiaDetector) IsNvidiaDPU(dpu *configv1.DataProcessingUnit) bool {
    switch dpu.Spec.DpuProductName {
    case "nvidia-bluefield-2", "nvidia-bluefield-3":
        return true
    default:
        return false
    }
}

// StatusReflectingAdapter is an optional interface that adapters can implement
// to reflect DPF resource status back into DataProcessingUnit.status.conditions.
type StatusReflectingAdapter interface {
    ReflectStatus(ctx context.Context, dpu *configv1.DataProcessingUnit) error
}

// SyncDPU implements NvidiaBackendAdapter by creating or updating a DPF DPUNode
// resource that corresponds to the given OPI DataProcessingUnit.
func (a *DpfAdapter) SyncDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
    logger := log.FromContext(ctx)

    dpunodeName := dpu.Spec.NodeName
    if dpunodeName == "" {
        return fmt.Errorf("DataProcessingUnit %s has empty nodeName", dpu.Name)
    }

    dpunode := &unstructured.Unstructured{}
    dpunode.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "dpf.dpu.nvidia.com",
        Version: "v1alpha1",
        Kind:    "DPUNode",
    })

    err := a.Get(ctx, client.ObjectKey{Name: dpunodeName, Namespace: a.DpfNamespace}, dpunode)
    if client.IgnoreNotFound(err) != nil {
        return fmt.Errorf("failed to get DPUNode %s: %w", dpunodeName, err)
    }

    if err == nil {
        logger.Info("DPUNode already exists, updating", "name", dpunodeName)
        dpunode.UnstructuredContent()["spec"] = buildDPUNodeSpec(dpu)
        if updateErr := a.Update(ctx, dpunode); updateErr != nil {
            return fmt.Errorf("failed to update DPUNode %s: %w", dpunodeName, updateErr)
        }
        return nil
    }

    logger.Info("Creating DPUNode for DataProcessingUnit", "dpunode", dpunodeName, "dpu", dpu.Name)

    dpunode.SetName(dpunodeName)
    dpunode.SetNamespace(a.DpfNamespace)
    dpunode.UnstructuredContent()["spec"] = buildDPUNodeSpec(dpu)

    if createErr := a.Create(ctx, dpunode); createErr != nil {
        return fmt.Errorf("failed to create DPUNode %s: %w", dpunodeName, createErr)
    }

    // Ensure a corresponding DPUDevice resource exists
    if err := a.ensureDPUDevice(ctx, dpu); err != nil {
        return fmt.Errorf("failed to ensure DPUDevice for DPU %s: %w", dpu.Name, err)
    }

    return nil
}

// ensureDPUDevice creates or updates a DPF DPUDevice resource for the given DPU.
func (a *DpfAdapter) ensureDPUDevice(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
    logger := log.FromContext(ctx)
    deviceName := dpu.Name

    device := &unstructured.Unstructured{}
    device.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "dpf.dpu.nvidia.com",
        Version: "v1alpha1",
        Kind:    "DPUDevice",
    })

    err := a.Get(ctx, client.ObjectKey{Name: deviceName, Namespace: a.DpfNamespace}, device)
    if client.IgnoreNotFound(err) != nil {
        return fmt.Errorf("failed to get DPUDevice %s: %w", deviceName, err)
    }

    if err == nil {
        logger.Info("DPUDevice already exists", "name", deviceName)
        return nil
    }

    logger.Info("Creating DPUDevice for DataProcessingUnit", "device", deviceName, "dpu", dpu.Name)

    device.SetName(deviceName)
    device.SetNamespace(a.DpfNamespace)
    device.UnstructuredContent()["spec"] = map[string]interface{}{
        "dpunode": dpu.Spec.NodeName,
    }

    if createErr := a.Create(ctx, device); createErr != nil {
        return fmt.Errorf("failed to create DPUDevice %s: %w", deviceName, createErr)
    }

    return nil
}

// CleanupDPU implements NvidiaBackendAdapter by deleting the DPF DPUNode
// resource associated with the given OPI DataProcessingUnit.
func (a *DpfAdapter) CleanupDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
    logger := log.FromContext(ctx)

    dpunodeName := dpu.Spec.NodeName
    if dpunodeName == "" {
        return nil
    }

    dpunode := &unstructured.Unstructured{}
    dpunode.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "dpf.dpu.nvidia.com",
        Version: "v1alpha1",
        Kind:    "DPUNode",
    })

    err := a.Get(ctx, client.ObjectKey{Name: dpunodeName, Namespace: a.DpfNamespace}, dpunode)
    if client.IgnoreNotFound(err) != nil {
        return fmt.Errorf("failed to get DPUNode %s for cleanup: %w", dpunodeName, err)
    }
    if err != nil {
        logger.Info("DPUNode already gone, nothing to clean up", "name", dpunodeName)
        return nil
    }

    logger.Info("Deleting DPUNode during cleanup", "name", dpunodeName)
    if deleteErr := a.Delete(ctx, dpunode); deleteErr != nil {
        return fmt.Errorf("failed to delete DPUNode %s: %w", dpunodeName, deleteErr)
    }

    // Also clean up the associated DPUDevice
    device := &unstructured.Unstructured{}
    device.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "dpf.dpu.nvidia.com",
        Version: "v1alpha1",
        Kind:    "DPUDevice",
    })

    deviceErr := a.Get(ctx, client.ObjectKey{Name: dpu.Name, Namespace: a.DpfNamespace}, device)
    if client.IgnoreNotFound(deviceErr) != nil {
        return fmt.Errorf("failed to get DPUDevice %s for cleanup: %w", dpu.Name, deviceErr)
    }
    if deviceErr == nil {
        logger.Info("Deleting DPUDevice during cleanup", "name", dpu.Name)
        if deleteErr := a.Delete(ctx, device); deleteErr != nil {
            return fmt.Errorf("failed to delete DPUDevice %s: %w", dpu.Name, deleteErr)
        }
    }

    return nil
}

// buildDPUNodeSpec constructs the spec for a DPF DPUNode from an OPI DataProcessingUnit.
func buildDPUNodeSpec(dpu *configv1.DataProcessingUnit) map[string]interface{} {
    return map[string]interface{}{
        "nodeRebootMethod": "gNOI",
        "node":             dpu.Spec.NodeName,
    }
}

// reflectDPFStatus implements StatusReflectingAdapter by copying relevant
// conditions from a DPF DPUNode into DataProcessingUnit.status.conditions
// and persisting them via Status().Update().
func (a *DpfAdapter) ReflectStatus(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
    dpunodeName := dpu.Spec.NodeName
    if dpunodeName == "" {
        return nil
    }

    dpunode := &unstructured.Unstructured{}
    dpunode.SetGroupVersionKind(schema.GroupVersionKind{
        Group:   "dpf.dpu.nvidia.com",
        Version: "v1alpha1",
        Kind:    "DPUNode",
    })

    err := a.Get(ctx, client.ObjectKey{Name: dpunodeName, Namespace: a.DpfNamespace}, dpunode)
    if client.IgnoreNotFound(err) != nil {
        return err
    }

    if err != nil {
        meta.SetStatusCondition(&dpu.Status.Conditions, metav1.Condition{
            Type:    "BackendNotReady",
            Status:  metav1.ConditionTrue,
            Reason:  "DPFNotInstalled",
            Message: "DPF operator or DPUNode not found",
        })
        return a.Status().Update(ctx, dpu)
    }

    phase, _, _ := unstructured.NestedString(dpunode.Object, "status", "phase")
    if phase == "Ready" {
        meta.SetStatusCondition(&dpu.Status.Conditions, metav1.Condition{
            Type:    "Provisioned",
            Status:  metav1.ConditionTrue,
            Reason:  "DPFProvisioningComplete",
            Message: "DPUNode is in Ready phase",
        })
    } else {
        meta.SetStatusCondition(&dpu.Status.Conditions, metav1.Condition{
            Type:    "Provisioning",
            Status:  metav1.ConditionTrue,
            Reason:  "DPFProvisioningInProgress",
            Message: fmt.Sprintf("DPUNode phase: %s", phase),
        })
    }

    return a.Status().Update(ctx, dpu)
}

// NvidiaDPUBackendReconciler reconciles DataProcessingUnit objects that
// represent NVIDIA BlueField DPUs and delegates vendor-specific logic to
// the NvidiaBackendAdapter.
type NvidiaDPUBackendReconciler struct {
    client.Client
    Scheme         *runtime.Scheme
    Adapter        NvidiaBackendAdapter
    VendorDetector VendorDetector
}

// NewNvidiaDPUBackendReconciler constructs a new reconciler instance.
func NewNvidiaDPUBackendReconciler(c client.Client, scheme *runtime.Scheme, adapter NvidiaBackendAdapter, detector VendorDetector) *NvidiaDPUBackendReconciler {
    return &NvidiaDPUBackendReconciler{
        Client:         c,
        Scheme:         scheme,
        Adapter:        adapter,
        VendorDetector: detector,
    }
}

// Reconcile implements the controller-runtime reconciliation loop for
// DataProcessingUnit resources.
func (r *NvidiaDPUBackendReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    // Fetch the DataProcessingUnit instance
    dpu := &configv1.DataProcessingUnit{}
    if err := r.Get(ctx, req.NamespacedName, dpu); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    logger.Info("Reconciling NVIDIA backend for DataProcessingUnit", "name", dpu.Name, "nodeName", dpu.Spec.NodeName, "product", dpu.Spec.DpuProductName)

    // Check if this is an NVIDIA DPU via the vendor detector
    if !r.isNvidiaDPU(dpu) {
        logger.Info("Skipping non-NVIDIA DPU", "product", dpu.Spec.DpuProductName)
        return ctrl.Result{}, nil
    }

    // Handle deletion via finalizer
    if !dpu.ObjectMeta.DeletionTimestamp.IsZero() {
        if err := r.handleDeletion(ctx, dpu, logger); err != nil {
            return ctrl.Result{}, err
        }
        return ctrl.Result{}, nil
    }

    // Ensure a finalizer is present so we can clean up DPF resources
    if addFinalizer(dpu) {
        if err := r.Update(ctx, dpu); err != nil {
            logger.Error(err, "failed to add finalizer to DataProcessingUnit")
            return ctrl.Result{}, err
        }
    }

    // Delegate desired state synchronization to the adapter
    if err := r.Adapter.SyncDPU(ctx, dpu); err != nil {
        logger.Error(err, "failed to sync NVIDIA backend for DataProcessingUnit")
        return ctrl.Result{}, err
    }

    // Reflect DPF status back into DataProcessingUnit.status.conditions
    if statusAdapter, ok := r.Adapter.(StatusReflectingAdapter); ok {
        if err := statusAdapter.ReflectStatus(ctx, dpu); err != nil {
            logger.Error(err, "failed to reflect DPF status into DataProcessingUnit")
            return ctrl.Result{}, err
        }
    }

    return ctrl.Result{}, nil
}

// isNvidiaDPU delegates vendor detection to the injected VendorDetector.
func (r *NvidiaDPUBackendReconciler) isNvidiaDPU(dpu *configv1.DataProcessingUnit) bool {
    if r.VendorDetector == nil {
        return false
    }
    return r.VendorDetector.IsNvidiaDPU(dpu)
}

// handleDeletion performs cleanup of backend resources then removes the finalizer.
func (r *NvidiaDPUBackendReconciler) handleDeletion(ctx context.Context, dpu *configv1.DataProcessingUnit, logger logr.Logger) error {
    logger.Info("Handling NVIDIA backend deletion for DataProcessingUnit", "name", dpu.Name)

    if err := r.Adapter.CleanupDPU(ctx, dpu); err != nil {
        logger.Error(err, "failed to clean up NVIDIA backend resources")
        return err
    }

    if removeFinalizer(dpu) {
        if err := r.Update(ctx, dpu); err != nil {
            logger.Error(err, "failed to remove finalizer from DataProcessingUnit")
            return err
        }
    }

    return nil
}

const nvidiaBackendFinalizer = "dpu.opi.io/nvidia-backend-finalizer"

// addFinalizer ensures the NVIDIA backend finalizer is present on the object.
func addFinalizer(dpu *configv1.DataProcessingUnit) bool {
    for _, f := range dpu.ObjectMeta.Finalizers {
        if f == nvidiaBackendFinalizer {
            return false
        }
    }
    dpu.ObjectMeta.Finalizers = append(dpu.ObjectMeta.Finalizers, nvidiaBackendFinalizer)
    return true
}

// removeFinalizer removes the NVIDIA backend finalizer from the object if present.
func removeFinalizer(dpu *configv1.DataProcessingUnit) bool {
    finalizers := dpu.ObjectMeta.Finalizers
    newFinalizers := make([]string, 0, len(finalizers))
    removed := false
    for _, f := range finalizers {
        if f == nvidiaBackendFinalizer {
            removed = true
            continue
        }
        newFinalizers = append(newFinalizers, f)
    }
    if removed {
        dpu.ObjectMeta.Finalizers = newFinalizers
    }
    return removed
}

// SetupWithManager registers the reconciler with the controller-runtime manager.
// Additional watches for DPF CRDs (DPUNode, DPUDevice, DPUSet) can be added
// via Owns() or Watches() once DPF Go types are imported.
func (r *NvidiaDPUBackendReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&configv1.DataProcessingUnit{}).
        Complete(r)
}

