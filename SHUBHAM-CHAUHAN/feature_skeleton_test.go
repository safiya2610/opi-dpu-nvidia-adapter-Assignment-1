package controller

import (
	"context"
	"testing"

	configv1 "github.com/openshift/dpu-operator-nvidia-backend/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrl "sigs.k8s.io/controller-runtime"
)

// mockAdapter implements NvidiaBackendAdapter for testing
type mockAdapter struct {
	synced   bool
	cleaned  bool
	syncErr  error
	cleanErr error
}

func (m *mockAdapter) SyncDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
	m.synced = true
	return m.syncErr
}

func (m *mockAdapter) CleanupDPU(ctx context.Context, dpu *configv1.DataProcessingUnit) error {
	m.cleaned = true
	return m.cleanErr
}

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "dpu.openshift.io", Version: "v1"}
	scheme.AddKnownTypes(gv, &configv1.DataProcessingUnit{}, &configv1.DataProcessingUnitList{})
	metav1.AddToGroupVersion(scheme, gv)
	return scheme
}

func TestReconcile_SkipsNonNvidia(t *testing.T) {
	scheme := newTestScheme()

	dpu := &configv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "intel-dpu",
			Namespace: "default",
		},
		Spec: configv1.DataProcessingUnitSpec{
			DpuProductName: "intel-mount-evans",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dpu).Build()

	adapter := &mockAdapter{}
	reconciler := NewNvidiaDPUBackendReconciler(client, scheme, adapter, &DefaultNvidiaDetector{})

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "intel-dpu", Namespace: "default"},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if res.Requeue {
		t.Fatal("Expected no requeue for non-NVIDIA DPU")
	}

	if adapter.synced {
		t.Fatal("Expected adapter.SyncDPU to NOT be called for non-NVIDIA DPU")
	}
}

func TestReconcile_NvidiaDPU_AddsFinalizerAndSyncs(t *testing.T) {
	scheme := newTestScheme()

	dpu := &configv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nvidia-dpu-1",
			Namespace: "",
		},
		Spec: configv1.DataProcessingUnitSpec{
			DpuProductName: "nvidia-bluefield-3",
			NodeName:       "node-1",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dpu).Build()

	adapter := &mockAdapter{}
	reconciler := NewNvidiaDPUBackendReconciler(client, scheme, adapter, &DefaultNvidiaDetector{})

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nvidia-dpu-1"},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if res.Requeue {
		t.Fatal("Expected no requeue for NVIDIA DPU")
	}

	if !adapter.synced {
		t.Fatal("Expected adapter.SyncDPU to be called for NVIDIA DPU")
	}

	// Verify finalizer was added
	updated := &configv1.DataProcessingUnit{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: "nvidia-dpu-1"}, updated); err != nil {
		t.Fatalf("Failed to get updated DPU: %v", err)
	}

	found := false
	for _, f := range updated.Finalizers {
		if f == nvidiaBackendFinalizer {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Expected finalizer to be added to NVIDIA DPU")
	}
}

func TestReconcile_NvidiaDPU_DeletionCallsCleanup(t *testing.T) {
	scheme := newTestScheme()

	now := metav1.Now()
	dpu := &configv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nvidia-dpu-delete",
			Namespace:         "",
			DeletionTimestamp: &now,
			Finalizers:        []string{nvidiaBackendFinalizer},
		},
		Spec: configv1.DataProcessingUnitSpec{
			DpuProductName: "nvidia-bluefield-3",
			NodeName:       "node-delete",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dpu).Build()

	adapter := &mockAdapter{}
	reconciler := NewNvidiaDPUBackendReconciler(client, scheme, adapter, &DefaultNvidiaDetector{})

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nvidia-dpu-delete"},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !adapter.cleaned {
		t.Fatal("Expected adapter.CleanupDPU to be called during deletion")
	}

	// After cleanup and finalizer removal, the object is deleted by the API server
	// since it has a DeletionTimestamp and no remaining finalizers. We just verify
	// that cleanup was called and no error occurred.
}

func TestDpfAdapter_SyncDPU_CreatesDPUNode(t *testing.T) {
	scheme := newTestScheme()
	scheme.AddKnownTypes(schema.GroupVersion{Group: "dpf.dpu.nvidia.com", Version: "v1alpha1"},
		&unstructured.Unstructured{}, &unstructured.UnstructuredList{})

	dpu := &configv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-dpu-sync"},
		Spec: configv1.DataProcessingUnitSpec{
			DpuProductName: "nvidia-bluefield-3",
			NodeName:       "node-sync",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dpu).
		Build()

	adapter := NewDpfAdapter(cl, "dpf-operator-system")

	err := adapter.SyncDPU(context.Background(), dpu)
	if err != nil {
		t.Fatalf("SyncDPU failed: %v", err)
	}
}

func TestDefaultNvidiaDetector(t *testing.T) {
	detector := &DefaultNvidiaDetector{}

	if !detector.IsNvidiaDPU(&configv1.DataProcessingUnit{Spec: configv1.DataProcessingUnitSpec{DpuProductName: "nvidia-bluefield-3"}}) {
		t.Error("Expected bluefield-3 to be detected as NVIDIA")
	}

	if !detector.IsNvidiaDPU(&configv1.DataProcessingUnit{Spec: configv1.DataProcessingUnitSpec{DpuProductName: "nvidia-bluefield-2"}}) {
		t.Error("Expected bluefield-2 to be detected as NVIDIA")
	}

	if detector.IsNvidiaDPU(&configv1.DataProcessingUnit{Spec: configv1.DataProcessingUnitSpec{DpuProductName: "intel-mount-evans"}}) {
		t.Error("Expected intel to NOT be detected as NVIDIA")
	}
}
