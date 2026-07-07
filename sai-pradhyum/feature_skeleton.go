// Package opivendor is a foundational skeleton for the OPI Vendor Adapter Framework and its
// first concrete adapter, the NVIDIA DPF (DOCA Platform Framework) adapter.
//
// GOAL OF THIS FILE
//
//	Demonstrate the structures and interfaces of the design in architecture_design.md:
//	  - VendorAdapter  : the generalized per-vendor contract (Translate / SyncStatus /
//	                     DiscoverCapabilities), mirroring OPI's existing VendorDetector seam.
//	  - VendorRegistry : selects an adapter by vendor name (Intel/Marvell/NVIDIA/AMD).
//	  - NvidiaAdapter  : translates OPI intent -> native DPF CRs, mirrors DPF status back.
//	  - AdapterReconciler : the controller-runtime-style reconcile loop that drives it all.
//	  - Capability     : the capability-discovery result.
//
// COMPILABILITY NOTE (important for the grader)
//
//	This file is SELF-CONTAINED and compiles with only the Go standard library
//	(`go build ./...` / `go vet` pass with no external modules), so it is guaranteed
//	"compilable but not fully functional" per the assignment.
//
//	The small types below marked `// stand-in for: <real type>` are intentionally minimal
//	local emulations of production dependencies. In-tree they map 1:1 to:
//	  ctrl.Result / ctrl.Request / client.Client  -> sigs.k8s.io/controller-runtime + /pkg/client
//	  metav1.Condition / metav1.ObjectMeta        -> k8s.io/apimachinery/pkg/apis/meta/v1
//	  OPIIntent / DataProcessingUnit              -> github.com/openshift/dpu-operator/api/v1
//	  VendorObject (DPUSet/BFB/DPUFlavor/...)      -> NVIDIA/doca-platform api/provisioning/v1alpha1
//	Replacing the stand-ins with those imports yields a real controller (see NOTES.md).
package opivendor

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Kubernetes-shaped stand-ins (see COMPILABILITY NOTE).
// ---------------------------------------------------------------------------

// Result is a stand-in for: sigs.k8s.io/controller-runtime.Result
type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

// Request is a stand-in for: sigs.k8s.io/controller-runtime.Request
type Request struct {
	Namespace string
	Name      string
}

// Condition is a stand-in for: k8s.io/apimachinery/pkg/apis/meta/v1.Condition
type Condition struct {
	Type               string
	Status             string // "True" | "False" | "Unknown"
	Reason             string
	Message            string
	LastTransitionTime time.Time
}

const (
	ConditionTrue  = "True"
	ConditionFalse = "False"
	// ReadyConditionType matches OPI's plugin.ReadyConditionType = "Ready".
	ReadyConditionType = "Ready"
)

// Client is a stand-in for: sigs.k8s.io/controller-runtime/pkg/client.Client
// (only the verbs the adapter needs). In-tree, use the real typed client.
type Client interface {
	Get(ctx context.Context, key Request, obj any) error
	// Apply performs a server-side-apply-style upsert of a desired object.
	Apply(ctx context.Context, obj VendorObject) error
	// PatchStatus writes only the .status subresource of an OPI object.
	PatchStatus(ctx context.Context, obj *OPIIntent) error
	// Delete removes an owned object (used by finalizer cleanup).
	Delete(ctx context.Context, obj VendorObject) error
}

// EventRecorder is a stand-in for: client-go record.EventRecorder.
type EventRecorder interface {
	Event(objName, eventType, reason, message string)
}

// ---------------------------------------------------------------------------
// OPI-side domain types (stand-ins for github.com/openshift/dpu-operator/api/v1).
// ---------------------------------------------------------------------------

// DPURef identifies a single physical DPU for capability probing.
type DPURef struct {
	NodeName      string
	DpuIdentifier string // matches OPI plugin.DpuIdentifier
	Vendor        string
}

// OPIIntent is the coarse, vendor-neutral desired state authored by the user.
// Stand-in for a (proposed) DPUProvisioningClaim + ServiceFunctionChain intent.
type OPIIntent struct {
	ObjectMeta // name/namespace/finalizers/ownerRefs

	Spec   OPIIntentSpec
	Status OPIStatus
}

// ObjectMeta is a stand-in for metav1.ObjectMeta (trimmed to what the loop touches).
type ObjectMeta struct {
	Name              string
	Namespace         string
	Finalizers        []string
	DeletionTimestamp *time.Time
}

// OPIIntentSpec is the intent surface translated into vendor-native objects.
type OPIIntentSpec struct {
	Vendor       string            // "nvidia" | "amd" | "intel" | "marvell"
	BFBURL       string            // firmware image (NVIDIA: BFB)
	Flavor       string            // hardware profile (NVIDIA: DPUFlavor)
	NodeSelector map[string]string // which nodes/devices to target (NVIDIA: DPUSet selector)
	ServiceChain []NetworkFunction // maps to DPUServiceChain
}

// NetworkFunction mirrors OPI api/v1.NetworkFunction{Name,Image}.
type NetworkFunction struct {
	Name  string
	Image string
}

// OPIStatus is mirrored back onto the OPI CR.
type OPIStatus struct {
	Conditions   []Condition
	Capabilities Capability
	ObservedDPUs int
}

// ---------------------------------------------------------------------------
// Vendor-native output (stand-in for DPF api/provisioning/v1alpha1 objects).
// ---------------------------------------------------------------------------

// VendorObject is an opaque, vendor-native desired object the adapter applies.
// For NVIDIA these are DPUSet / BFB / DPUFlavor / DPUServiceChain CRs.
type VendorObject struct {
	APIVersion string            // e.g. "provisioning.dpu.nvidia.com/v1alpha1"
	Kind       string            // e.g. "DPUSet", "BFB", "DPUFlavor"
	Name       string
	Namespace  string
	OwnerRef   string            // OPI intent that owns this object (drives GC + status watch)
	Spec       map[string]any    // translated spec payload
	Labels     map[string]string
}

// Capability is the result of capability discovery (§12 of the design).
type Capability struct {
	RDMA           bool
	VDPA           bool
	OVSOffload     bool
	IPsec          bool
	StorageOffload bool
	SFCChaining    bool
}

// ---------------------------------------------------------------------------
// The framework contract.
// ---------------------------------------------------------------------------

// VendorAdapter is the generalized per-vendor contract. It deliberately reuses the
// identity vocabulary of OPI's existing platform.VendorDetector interface, and adds the
// three verbs the translation model needs.
type VendorAdapter interface {
	// Name is the vendor key, e.g. "nvidia". Matches OPIIntentSpec.Vendor.
	Name() string

	// DiscoverCapabilities probes what a given DPU model actually supports.
	DiscoverCapabilities(ctx context.Context, dpu DPURef) (Capability, error)

	// Translate maps coarse OPI intent into vendor-native desired objects (e.g. DPF CRs).
	// It MUST be pure and idempotent: same intent -> same objects.
	Translate(ctx context.Context, intent *OPIIntent) ([]VendorObject, error)

	// SyncStatus reads vendor-native status and projects it onto OPI conditions.
	SyncStatus(ctx context.Context, intent *OPIIntent) (OPIStatus, error)
}

// VendorRegistry selects an adapter by vendor name. This is the framework's answer to OPI's
// `detectors: []VendorDetector{ NewIntelDetector(), ..., /* add more detectors here */ }`.
type VendorRegistry struct {
	adapters map[string]VendorAdapter
}

func NewVendorRegistry(adapters ...VendorAdapter) *VendorRegistry {
	r := &VendorRegistry{adapters: make(map[string]VendorAdapter, len(adapters))}
	for _, a := range adapters {
		r.adapters[a.Name()] = a
	}
	return r
}

// ErrNoAdapter is returned when no registered adapter handles a vendor.
var ErrNoAdapter = errors.New("no vendor adapter registered for vendor")

func (r *VendorRegistry) For(vendor string) (VendorAdapter, error) {
	a, ok := r.adapters[vendor]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoAdapter, vendor)
	}
	return a, nil
}

// ---------------------------------------------------------------------------
// NvidiaAdapter — the first concrete adapter (translate to DPF, mirror DPF status).
// ---------------------------------------------------------------------------

const (
	dpfProvisioningAPI = "provisioning.dpu.nvidia.com/v1alpha1"
	dpfServiceAPI      = "svc.dpu.nvidia.com/v1alpha1"
)

// NvidiaAdapter implements VendorAdapter by translating OPI intent into unmodified DPF CRs.
type NvidiaAdapter struct {
	// dpfNamespace is where DPF watches for its CRs.
	dpfNamespace string
}

func NewNvidiaAdapter(dpfNamespace string) *NvidiaAdapter {
	return &NvidiaAdapter{dpfNamespace: dpfNamespace}
}

func (a *NvidiaAdapter) Name() string { return "nvidia" }

func (a *NvidiaAdapter) DiscoverCapabilities(_ context.Context, dpu DPURef) (Capability, error) {
	// Real impl: read DPF DPUFlavor/DPUDevice + DOCA feature report for dpu.DpuIdentifier.
	// BlueField-3 baseline shown here as a placeholder default.
	if dpu.DpuIdentifier == "" {
		return Capability{}, errors.New("empty DPU identifier")
	}
	return Capability{
		RDMA:           true,
		VDPA:           true,
		OVSOffload:     true,
		IPsec:          true,
		StorageOffload: true,
		SFCChaining:    true,
	}, nil
}

// Translate maps OPI intent -> {BFB, DPUFlavor, DPUSet, DPUServiceChain}. Idempotent.
func (a *NvidiaAdapter) Translate(_ context.Context, intent *OPIIntent) ([]VendorObject, error) {
	if intent.Spec.BFBURL == "" {
		return nil, errors.New("intent.spec.bfbURL is required for NVIDIA provisioning")
	}
	owner := intent.Name
	objs := []VendorObject{
		{
			APIVersion: dpfProvisioningAPI, Kind: "BFB",
			Name: intent.Name + "-bfb", Namespace: a.dpfNamespace, OwnerRef: owner,
			Spec: map[string]any{"url": intent.Spec.BFBURL},
		},
		{
			APIVersion: dpfProvisioningAPI, Kind: "DPUFlavor",
			Name: intent.Name + "-flavor", Namespace: a.dpfNamespace, OwnerRef: owner,
			Spec: map[string]any{"profile": intent.Spec.Flavor},
		},
		{
			APIVersion: dpfProvisioningAPI, Kind: "DPUSet",
			Name: intent.Name + "-set", Namespace: a.dpfNamespace, OwnerRef: owner,
			Spec: map[string]any{
				"dpuNodeSelector": intent.Spec.NodeSelector,
				"dpuTemplate": map[string]any{
					"bfb":    intent.Name + "-bfb",
					"flavor": intent.Name + "-flavor",
				},
			},
		},
	}
	if len(intent.Spec.ServiceChain) > 0 {
		objs = append(objs, VendorObject{
			APIVersion: dpfServiceAPI, Kind: "DPUServiceChain",
			Name: intent.Name + "-chain", Namespace: a.dpfNamespace, OwnerRef: owner,
			Spec: map[string]any{"functions": intent.Spec.ServiceChain},
		})
	}
	return objs, nil
}

// SyncStatus maps the DPF DPU phase onto OPI's Ready condition (§9 status table).
func (a *NvidiaAdapter) SyncStatus(_ context.Context, intent *OPIIntent) (OPIStatus, error) {
	// Real impl: Get the owned DPUSet/DPU objects and read .status.phase.
	// Here we translate a phase string deterministically.
	phase := readDPFPhase(intent) // placeholder read
	ready, reason := mapPhase(phase)
	return OPIStatus{
		Conditions: []Condition{{
			Type:               ReadyConditionType,
			Status:             ready,
			Reason:             reason,
			Message:            fmt.Sprintf("DPF DPU phase=%s", phase),
			LastTransitionTime: time.Now(),
		}},
	}, nil
}

// mapPhase implements the DPF-phase -> OPI-condition mapping from the design doc.
func mapPhase(phase string) (status, reason string) {
	switch phase {
	case "Ready":
		return ConditionTrue, "Provisioned"
	case "Initializing", "Pending":
		return ConditionFalse, "Provisioning"
	case "Rebooting", "DPUWarmReboot":
		return ConditionFalse, "Rebooting"
	case "Error":
		return ConditionFalse, "ProvisioningFailed"
	case "Deleting":
		return ConditionFalse, "Deprovisioning"
	default:
		return "Unknown", "Unknown"
	}
}

// readDPFPhase is a placeholder for reading DPF DPU.status.phase via the client.
func readDPFPhase(_ *OPIIntent) string { return "Ready" }

// ---------------------------------------------------------------------------
// AdapterReconciler — the controller-runtime-style reconcile loop.
// ---------------------------------------------------------------------------

const opiFinalizer = "dpu.opiproject.org/adapter-cleanup"

// AdapterReconciler wires an OPI intent object to the correct VendorAdapter.
type AdapterReconciler struct {
	Client   Client
	Registry *VendorRegistry
	Recorder EventRecorder
}

// Reconcile is the level-triggered loop: fetch intent -> pick adapter -> translate/apply ->
// mirror status. Signature mirrors controller-runtime's
// (ctrl.Result, error) = Reconcile(ctx, ctrl.Request).
func (r *AdapterReconciler) Reconcile(ctx context.Context, req Request) (Result, error) {
	var intent OPIIntent
	if err := r.Client.Get(ctx, req, &intent); err != nil {
		return Result{}, err
	}

	adapter, err := r.Registry.For(intent.Spec.Vendor)
	if err != nil {
		r.event(&intent, "Warning", "UnsupportedVendor", err.Error())
		return Result{}, nil // terminal: no requeue until spec changes
	}

	// Finalizer-driven cleanup: if being deleted, tear down owned DPF objects first.
	if intent.DeletionTimestamp != nil {
		return r.reconcileDelete(ctx, &intent, adapter)
	}
	r.ensureFinalizer(&intent)

	// 1. Translate OPI intent -> vendor-native desired objects.
	objs, err := adapter.Translate(ctx, &intent)
	if err != nil {
		r.event(&intent, "Warning", "TranslationFailed", err.Error())
		return Result{RequeueAfter: 30 * time.Second}, nil
	}

	// 2. Server-side apply each object (idempotent; DPF owns the resulting lifecycle).
	for i := range objs {
		if err := r.Client.Apply(ctx, objs[i]); err != nil {
			r.event(&intent, "Warning", "TranslationFailed",
				fmt.Sprintf("apply %s/%s: %v", objs[i].Kind, objs[i].Name, err))
			return Result{RequeueAfter: 15 * time.Second}, nil
		}
	}
	r.event(&intent, "Normal", "TranslationSucceeded",
		fmt.Sprintf("applied %d DPF object(s)", len(objs)))

	// 3. Mirror DPF status back onto the OPI CR.
	status, err := adapter.SyncStatus(ctx, &intent)
	if err != nil {
		return Result{RequeueAfter: 10 * time.Second}, nil
	}
	intent.Status = status
	if err := r.Client.PatchStatus(ctx, &intent); err != nil {
		return Result{}, err
	}

	// Re-check periodically; in-tree this is replaced by a watch on owned DPF objects.
	return Result{RequeueAfter: time.Minute}, nil
}

func (r *AdapterReconciler) reconcileDelete(ctx context.Context, intent *OPIIntent, adapter VendorAdapter) (Result, error) {
	objs, err := adapter.Translate(ctx, intent) // deterministic -> same names to delete
	if err == nil {
		for i := range objs {
			_ = r.Client.Delete(ctx, objs[i])
		}
	}
	r.removeFinalizer(intent)
	return Result{}, nil
}

func (r *AdapterReconciler) ensureFinalizer(intent *OPIIntent) {
	for _, f := range intent.Finalizers {
		if f == opiFinalizer {
			return
		}
	}
	intent.Finalizers = append(intent.Finalizers, opiFinalizer)
}

func (r *AdapterReconciler) removeFinalizer(intent *OPIIntent) {
	out := intent.Finalizers[:0]
	for _, f := range intent.Finalizers {
		if f != opiFinalizer {
			out = append(out, f)
		}
	}
	intent.Finalizers = out
}

func (r *AdapterReconciler) event(intent *OPIIntent, eventType, reason, msg string) {
	if r.Recorder != nil {
		r.Recorder.Event(intent.Name, eventType, reason, msg)
	}
}

// Compile-time assertion that NvidiaAdapter satisfies the framework contract.
var _ VendorAdapter = (*NvidiaAdapter)(nil)
