// Package adapter contains the foundational structures and interfaces for the
// opi-nvidia-adapter component described in architecture_design.md.
//
// The adapter is a Translation-Adapter Operator that mediates between the
// vendor-neutral OPI DpuOperatorConfig CRD and NVIDIA's unmodified,
// upstream DPF Operator CRDs (BFB, DPUFlavor, DPUSet, DPU, DPUCluster,
// DPUDiscovery under provisioning.dpu.nvidia.com). It never reimplements
// DPF's provisioning logic; it only translates spec down and reflects
// status up.
//
// DEPENDENCY NOTE: this file is intentionally self-contained and depends
// only on the Go standard library. In a real implementation, the types in
// the "Local stand-ins for external Kubernetes/controller-runtime types"
// section below would instead be imported from k8s.io/apimachinery,
// k8s.io/client-go, and sigs.k8s.io/controller-runtime. They are
// reimplemented here at minimal fidelity — matching only the fields and
// method signatures this skeleton's control flow actually touches — purely
// so the file compiles with `go build` out of the box, with no go.mod,
// no module proxy access, and no vendored dependencies required. Swap the
// stand-ins for the real imports (same names/shapes are used deliberately)
// once this lives inside an actual controller-runtime project scaffolded
// with kubebuilder.
//
// This file is a SKELETON: it establishes types, interfaces, constants,
// and controller wiring with realistic control flow, but stub method
// bodies do not perform real cluster I/O. It is intended to compile and to
// communicate architectural shape, not to run in production.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Local stand-ins for external Kubernetes/controller-runtime types
// ---------------------------------------------------------------------------
//
// See the DEPENDENCY NOTE above. Each type here documents which real
// package/type it stands in for.

// NamespacedName stands in for k8s.io/apimachinery/pkg/types.NamespacedName.
type NamespacedName struct {
	Namespace string
	Name      string
}

// String matches the real types.NamespacedName's String() convention.
func (n NamespacedName) String() string {
	return n.Namespace + "/" + n.Name
}

// UID stands in for k8s.io/apimachinery/pkg/types.UID.
type UID string

// ObjectMeta stands in for a minimal subset of
// k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta — only the fields this
// skeleton's control flow reads or writes.
type ObjectMeta struct {
	Name              string
	Namespace         string
	UID               UID
	Generation        int64
	Annotations       map[string]string
	Labels            map[string]string
	OwnerReferences   []OwnerReference
	DeletionTimestamp *time.Time
}

// OwnerReference stands in for metav1.OwnerReference. The adapter uses this
// alongside LabelSourceUID: owner references give Kubernetes a native
// relationship, while the source UID label gives controllers a stable query
// key for bottom-up finalizer/deadlock logic.
type OwnerReference struct {
	APIVersion string
	Kind       string
	Name       string
	UID        UID
}

// Condition stands in for k8s.io/apimachinery/pkg/apis/meta/v1.Condition.
type Condition struct {
	Type               string
	Status             string // "True" | "False" | "Unknown"
	Reason             string
	Message            string
	ObservedGeneration int64
	LastTransitionTime time.Time
}

// EventType stands in for the EventTypeNormal/EventTypeWarning constants
// declared on k8s.io/api/core/v1.
type EventType string

const (
	// EventTypeNormal stands in for corev1.EventTypeNormal.
	EventTypeNormal EventType = "Normal"
	// EventTypeWarning stands in for corev1.EventTypeWarning.
	EventTypeWarning EventType = "Warning"
)

// Object stands in for sigs.k8s.io/controller-runtime/pkg/client.Object —
// the minimal interface every watched/managed resource must satisfy.
type Object interface {
	GetName() string
	GetNamespace() string
	GetUID() UID
}

// EventRecorder stands in for k8s.io/client-go/tools/record.EventRecorder.
type EventRecorder interface {
	// Eventf records a structured event against the given object.
	Eventf(object Object, eventType EventType, reason, messageFmt string, args ...interface{})
}

// ErrNotFound stands in for the sentinel apierrors.IsNotFound(err) checks
// against. A real implementation uses apierrors.NewNotFound / IsNotFound;
// here IsNotFound below wraps errors.Is against this sentinel instead.
var ErrNotFound = errors.New("object not found")

// IsNotFound stands in for k8s.io/apimachinery/pkg/api/errors.IsNotFound.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// Client stands in for sigs.k8s.io/controller-runtime/pkg/client.Client —
// only the single method this skeleton's Reconcile loops call directly.
type Client interface {
	// Get stands in for client.Client.Get.
	Get(ctx context.Context, key NamespacedName, out Object) error
}

// Request stands in for sigs.k8s.io/controller-runtime.Request.
type Request struct {
	NamespacedName
}

// Result stands in for sigs.k8s.io/controller-runtime.Result.
type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

// Manager stands in for sigs.k8s.io/controller-runtime.Manager. It is left
// as an empty interface here since this skeleton never calls a method on
// it directly — SetupWithManager only needs to accept something of this
// shape to preserve the real kubebuilder-generated call signature.
type Manager interface{}

// Reconciler stands in for sigs.k8s.io/controller-runtime/pkg/reconcile.Reconciler.
type Reconciler interface {
	Reconcile(ctx context.Context, req Request) (Result, error)
}

// EventFilter stands in for sigs.k8s.io/controller-runtime/pkg/predicate.Predicate.
type EventFilter interface {
	// name unexported deliberately: this is a marker interface in the
	// skeleton, matching the shape (not the full method set) of the real
	// predicate.Predicate interface.
	isEventFilter()
}

// GenerationChangedPredicate stands in for
// sigs.k8s.io/controller-runtime/pkg/predicate.GenerationChangedPredicate.
// Used by TranslatorController per §1.4.10 so that status-only updates
// (which do not bump .metadata.generation) cannot re-trigger translation.
type GenerationChangedPredicate struct{}

func (GenerationChangedPredicate) isEventFilter() {}

// controllerBuilder stands in for the fluent builder returned by
// sigs.k8s.io/controller-runtime.NewControllerManagedBy(mgr).
type controllerBuilder struct {
	watched Object
	filters []EventFilter
}

// NewControllerManagedBy stands in for ctrl.NewControllerManagedBy.
func NewControllerManagedBy(_ Manager) *controllerBuilder {
	return &controllerBuilder{}
}

// For stands in for the builder's For(...) method.
func (b *controllerBuilder) For(obj Object) *controllerBuilder {
	b.watched = obj
	return b
}

// WithEventFilter stands in for the builder's WithEventFilter(...) method.
func (b *controllerBuilder) WithEventFilter(f EventFilter) *controllerBuilder {
	b.filters = append(b.filters, f)
	return b
}

// Complete stands in for the builder's Complete(...) method, which in the
// real controller-runtime registers the Reconciler with the manager's
// controller machinery. Here it is a no-op that returns nil, since this
// skeleton never actually starts a manager.
func (b *controllerBuilder) Complete(_ Reconciler) error {
	return nil
}

// ---------------------------------------------------------------------------
// Condition types (architecture §1.4)
// ---------------------------------------------------------------------------

// ConditionType is a typed alias for the standard Kubernetes condition type
// string, restricted to the vocabulary this adapter is permitted to emit.
// Keeping this as a distinct type (rather than a bare string) prevents an
// arbitrary string literal from being patched onto a resource's status by
// accident elsewhere in the codebase.
type ConditionType string

const (
	// ConditionTranslationComplete is set true only once every expected DPF
	// child object for a given OPI CR exists and reports at least
	// Provisioning status. See architecture §1.4.3 (non-atomic fan-out).
	ConditionTranslationComplete ConditionType = "TranslationComplete"

	// ConditionPendingHardwareDiscovery indicates the Translator has
	// deferred creating a DPUSet entry for one or more nodes because no
	// matching DPF DPUDiscovery resource was found yet. See §1.4.9.
	ConditionPendingHardwareDiscovery ConditionType = "PendingHardwareDiscovery"

	// ConditionHardwareDiscoveryFailed is the terminal escalation of
	// ConditionPendingHardwareDiscovery once the discovery timeout (default
	// 15 minutes) elapses for a specific node. See §1.4.9.
	ConditionHardwareDiscoveryFailed ConditionType = "HardwareDiscoveryFailed"

	// ConditionStatusStale exposes the age of the last successful
	// cross-cluster status observation. Set alongside Unknown, never
	// directly False, per §1.4.4.
	ConditionStatusStale ConditionType = "StatusStale"

	// ConditionTenantAuthFailure is a distinct condition for tenant-cluster
	// authentication failures (401/403), kept separate from generic
	// staleness so a real credential problem cannot hide behind reboot
	// tolerance. See §1.4.4 and §1.4.12.
	ConditionTenantAuthFailure ConditionType = "TenantAuthFailure"

	// ConditionTenantEndpointChanged fires when the tenant cluster's
	// control-plane endpoint has changed (e.g. Kamaji TenantControlPlane
	// rescheduled) and is checked BEFORE reboot-tolerant staleness logic
	// is allowed to apply. See §1.4.12.
	ConditionTenantEndpointChanged ConditionType = "TenantEndpointChanged"

	// ConditionManualOverrideActive is surfaced when an operator has
	// applied the opi.io/unmanaged annotation to intentionally exempt an
	// object from drift reconciliation. See §1.4.7.
	ConditionManualOverrideActive ConditionType = "ManualOverrideActive"

	// ConditionUnsupportedOPIVersion indicates the Version-Compatibility
	// Controller has no translation profile for the currently served OPI
	// CRD schema version. Because the adapter cannot safely write status
	// onto a resource whose schema it does not understand, this condition
	// is emitted as a Kubernetes Event, not a CR status patch. See §1.4.8.
	ConditionUnsupportedOPIVersion ConditionType = "UnsupportedOPIVersion"

	// ConditionUnsupportedDPFVersion is the DPF-side analogue of
	// ConditionUnsupportedOPIVersion. Unlike the OPI-side case, this CAN be
	// written onto the OPI CR's status, since the OPI schema is understood
	// even when the DPF schema is not.
	ConditionUnsupportedDPFVersion ConditionType = "UnsupportedDPFVersion"

	// ConditionDPFOperatorUnresponsive is the terminal condition set by the
	// Deadlock-Recovery mechanism once DPF child objects have remained
	// Terminating beyond the timeout AND the DPF Operator itself is
	// confirmed absent. See §1.4.6.
	ConditionDPFOperatorUnresponsive ConditionType = "DPFOperatorUnresponsive"

	// ConditionDeletionBlocked is a general-purpose condition surfacing why
	// an OPI CR's deletion has not yet completed, with a human-readable
	// reason attached. Used both for the normal in-flight-hardware case
	// (§1.4.5) and the deadlock fallback (§1.4.6).
	ConditionDeletionBlocked ConditionType = "DeletionBlocked"
)

// Annotation and label keys used by the adapter. Centralized here so a typo
// in one controller cannot silently diverge from another's expectations.
const (
	// AnnotationForceCleanup is the human-applied emergency escape hatch
	// from §1.4.6. Its presence authorizes the Deadlock-Recovery mechanism
	// to strip DPF's own finalizers from permanently stuck child objects.
	// It is NEVER set automatically by any controller in this package.
	AnnotationForceCleanup = "opi.io/force-cleanup"

	// AnnotationUnmanaged is the escape hatch from §1.4.7 that exempts a
	// specific DPF child object from drift reconciliation.
	AnnotationUnmanaged = "opi.io/unmanaged"

	// LabelManagedBy identifies DPF objects created by this adapter.
	LabelManagedBy = "opi.io/managed-by"

	// LabelSourceUID correlates DPF child objects back to the owning OPI
	// CR's UID, independent of Kubernetes owner references (see
	// architecture_design.md §1.3 on why owner references alone are
	// insufficient here).
	LabelSourceUID = "opi.io/source-uid"

	// ManagedByValue is the constant value written into LabelManagedBy.
	ManagedByValue = "opi-nvidia-adapter"

	// FinalizerAdapterCleanup is the finalizer this adapter places on OPI
	// CRs to enforce the bottom-up deletion chain of §1.4.5 and §1.4.6.
	FinalizerAdapterCleanup = "opi.io/nvidia-adapter-cleanup"

	// FieldManagerAdapter is the stable server-side-apply field manager name
	// required by §1.4.2 and §1.4.3.
	FieldManagerAdapter = "opi-nvidia-adapter"
)

// Tunable defaults referenced throughout the architecture document. Exposed
// as variables (not consts) so they can be overridden in tests or via
// operator configuration without touching call sites.
var (
	// DeadlockTimeout is the generous Terminating-age threshold from §1.4.6
	// (default 30 minutes) beyond which the Deadlock-Recovery mechanism
	// begins checking DPF Operator liveness.
	DeadlockTimeout = 30 * time.Minute

	// HardwareDiscoveryTimeout is the per-node timeout from §1.4.9 after
	// which PendingHardwareDiscovery escalates to HardwareDiscoveryFailed.
	HardwareDiscoveryTimeout = 15 * time.Minute

	// StatusStaleFailureThreshold is the consecutive-failed-probe count
	// from §1.4.4 required before downgrading Unknown to False.
	StatusStaleFailureThreshold = 3

	// StatusStaleProbeWindow is the minimum time window over which
	// StatusStaleFailureThreshold must be observed, per §1.4.4.
	StatusStaleProbeWindow = 90 * time.Second

	// DriftReconcileInterval is the periodic re-list interval from §1.4.7.
	DriftReconcileInterval = 5 * time.Minute
)

// ---------------------------------------------------------------------------
// Version identifiers and the two-axis translation matrix (§1.4.8)
// ---------------------------------------------------------------------------

// SchemaVersion identifies a served API version for either the OPI or the
// DPF CRD group (e.g. "v1alpha1", "v1beta1").
type SchemaVersion string

// VersionPair is the composite key into the translation profile matrix,
// deliberately two-dimensional per §1.4.8 rather than DPF-version-only.
type VersionPair struct {
	OPIVersion SchemaVersion
	DPFVersion SchemaVersion
}

// String implements fmt.Stringer for readable log lines and Event messages.
func (v VersionPair) String() string {
	return fmt.Sprintf("opi=%s,dpf=%s", v.OPIVersion, v.DPFVersion)
}

// DpuOperatorConfigSpec is a minimal local stand-in for the real OPI CRD's
// spec type, sufficient to give the Translator interface a concrete shape.
// In the real integration this would be imported from the OPI project's
// generated client types rather than redefined here.
type DpuOperatorConfigSpec struct {
	Vendor          string
	Mode            string
	NodeSelector    map[string]string
	FirmwareBundle  FirmwareBundleRef
	OffloadServices []string
}

// FirmwareBundleRef points at a BFB image to translate into a DPF BFB CR.
type FirmwareBundleRef struct {
	URI string
}

// DpuOperatorConfigStatus mirrors the aggregated condition set the
// architecture defines for the OPI CR's status subresource.
type DpuOperatorConfigStatus struct {
	Conditions         []Condition
	ObservedGeneration int64
}

// DpuOperatorConfig is a minimal local stand-in for the real OPI CRD
// object, implementing the local Object interface above. In the real
// integration this would be the OPI project's generated, deepcopy-capable
// type instead.
type DpuOperatorConfig struct {
	ObjectMeta
	Spec   DpuOperatorConfigSpec
	Status DpuOperatorConfigStatus
}

// GetName implements Object.
func (d *DpuOperatorConfig) GetName() string { return d.ObjectMeta.Name }

// GetNamespace implements Object.
func (d *DpuOperatorConfig) GetNamespace() string { return d.ObjectMeta.Namespace }

// GetUID implements Object.
func (d *DpuOperatorConfig) GetUID() UID { return d.ObjectMeta.UID }

// ---------------------------------------------------------------------------
// Translator interface and registry (Strategy pattern, §1.4.8)
// ---------------------------------------------------------------------------

// DPFChildObject is a minimal stand-in for sigs.k8s.io/controller-runtime's
// client.Object as returned by a Translator — enough shape to be labeled,
// owned, and server-side-applied by TranslatorController.
type DPFChildObject struct {
	ObjectMeta
	Kind       string // e.g. "BFB", "DPUFlavor", "DPUSet"
	APIVersion string // e.g. "provisioning.dpu.nvidia.com/v1alpha1" — TypeMeta stand-in
}

// GetName implements Object.
func (o *DPFChildObject) GetName() string { return o.ObjectMeta.Name }

// GetNamespace implements Object.
func (o *DPFChildObject) GetNamespace() string { return o.ObjectMeta.Namespace }

// GetUID implements Object.
func (o *DPFChildObject) GetUID() UID { return o.ObjectMeta.UID }

// Translator converts a validated OPI DpuOperatorConfig spec into the
// complete set of DPF child objects required to satisfy it. Implementations
// MUST be idempotent: every call re-derives the full desired child-object
// set from spec, per §1.4.3, rather than assuming prior partial state.
type Translator interface {
	// Translate returns the complete desired set of DPF objects for the
	// given OPI spec. Implementations must not perform partial, one-shot
	// creation; callers apply the full returned set via server-side apply.
	Translate(ctx context.Context, spec *DpuOperatorConfigSpec) ([]*DPFChildObject, error)

	// SupportedVersions reports the (OPI, DPF) version pair this
	// implementation was written against, used by TranslatorRegistry for
	// profile selection.
	SupportedVersions() VersionPair
}

// ErrNoTranslationProfile is returned by TranslatorRegistry.Select when no
// registered Translator matches the discovered version pair. Callers MUST
// treat this as a terminal, non-retryable condition for the current
// generation rather than looping indefinitely — see §1.4.7 and §1.4.8.
var ErrNoTranslationProfile = errors.New("no translation profile registered for the discovered version pair")

// TranslatorRegistry selects the correct Translator implementation for a
// live (OPI version, DPF version) pair, implementing the two-axis matrix
// described in §1.4.8. It intentionally has no fallback "best effort" path:
// an unmatched pair is a hard stop, never a guess.
type TranslatorRegistry struct {
	profiles map[VersionPair]Translator
}

// NewTranslatorRegistry constructs an empty registry ready for profile
// registration via Register.
func NewTranslatorRegistry() *TranslatorRegistry {
	return &TranslatorRegistry{
		profiles: make(map[VersionPair]Translator),
	}
}

// Register adds a Translator implementation to the registry, keyed by the
// version pair it declares support for. Registering two translators for the
// same pair is a programming error and returns an error rather than
// silently overwriting the first registration.
func (r *TranslatorRegistry) Register(t Translator) error {
	key := t.SupportedVersions()
	if _, exists := r.profiles[key]; exists {
		return fmt.Errorf("translator already registered for version pair %s", key)
	}
	r.profiles[key] = t
	return nil
}

// Select returns the Translator registered for the given version pair, or
// ErrNoTranslationProfile if none matches. Callers in
// VersionCompatibilityController are responsible for turning
// ErrNoTranslationProfile into the appropriate UnsupportedOPIVersion /
// UnsupportedDPFVersion signal per §1.4.8.
func (r *TranslatorRegistry) Select(pair VersionPair) (Translator, error) {
	t, ok := r.profiles[pair]
	if !ok {
		return nil, ErrNoTranslationProfile
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// SchemaDiscoverer: live API discovery for both OPI and DPF CRDs (§1.4.8)
// ---------------------------------------------------------------------------

// SchemaDiscoverer performs live discovery of the currently served CRD
// versions for both the OPI and DPF API groups. A real implementation would
// wrap client-go's discovery client and/or watch CustomResourceDefinition
// objects directly; this interface exists so controllers can be tested
// against a fake discoverer without a live API server.
type SchemaDiscoverer interface {
	// DiscoverOPIVersion returns the currently served storage version of
	// the OPI DpuOperatorConfig CRD.
	DiscoverOPIVersion(ctx context.Context) (SchemaVersion, error)

	// DiscoverDPFVersion returns the currently served storage version of
	// the DPF provisioning.dpu.nvidia.com CRD group.
	DiscoverDPFVersion(ctx context.Context) (SchemaVersion, error)
}

// ActiveTranslatorRef is a thread-safe holder for the currently active
// Translator, written by VersionCompatibilityController and read by
// TranslatorController. This eliminates the dual-discovery race identified
// in audit item B-4 by making VersionCompatibilityController the single
// source of truth for which translation profile is current.
type ActiveTranslatorRef struct {
	mu         sync.Mutex
	translator Translator
}

// Set stores a new active translator. Called only by
// VersionCompatibilityController after a successful version-pair resolution.
func (a *ActiveTranslatorRef) Set(t Translator) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.translator = t
}

// Clear removes the active translator (e.g., when the version pair becomes
// unsupported). Called by VersionCompatibilityController.
func (a *ActiveTranslatorRef) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.translator = nil
}

// Get returns the currently active translator, or nil if no compatible
// profile is resolved. Called by TranslatorController.
func (a *ActiveTranslatorRef) Get() Translator {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.translator
}

// ---------------------------------------------------------------------------
// DPF watched-resource stand-in types (B-1/B-2/B-3 audit fix)
// ---------------------------------------------------------------------------
//
// These types stand in for the real DPF CRD types that each non-Translator
// controller watches. In the original skeleton, every controller's
// SetupWithManager used DpuOperatorConfig as the For target — violating
// §1.4.10 which mandates that only the Translator watches the OPI CR.
// These stand-ins make the wiring structurally correct.

// DPUSet stands in for provisioning.dpu.nvidia.com/DPUSet, the primary
// DPF resource the StatusAggregatorController and DriftReconciliation-
// Controller watch.
type DPUSet struct {
	ObjectMeta
}

func (d *DPUSet) GetName() string      { return d.ObjectMeta.Name }
func (d *DPUSet) GetNamespace() string  { return d.ObjectMeta.Namespace }
func (d *DPUSet) GetUID() UID           { return d.ObjectMeta.UID }

// CRDObject stands in for apiextensionsv1.CustomResourceDefinition, the
// resource the VersionCompatibilityController watches to detect schema
// changes in both the OPI and DPF API groups. See §1.4.8.
type CRDObject struct {
	ObjectMeta
}

func (c *CRDObject) GetName() string      { return c.ObjectMeta.Name }
func (c *CRDObject) GetNamespace() string  { return c.ObjectMeta.Namespace }
func (c *CRDObject) GetUID() UID           { return c.ObjectMeta.UID }

// ---------------------------------------------------------------------------
// NodeLister: resolves a label selector to concrete node names (B-5 fix)
// ---------------------------------------------------------------------------

// NodeLister abstracts listing Kubernetes Node objects that match a label
// selector. In a real implementation this wraps a client-go informer or
// lister. The TranslatorController uses this to resolve NodeSelector label
// keys/values into actual node names before performing the hardware
// pre-flight check from §1.4.9.
type NodeLister interface {
	// ListMatchingNodes returns the names of all Nodes whose labels match
	// every key=value pair in the given selector map.
	ListMatchingNodes(ctx context.Context, selector map[string]string) ([]string, error)
}

// ---------------------------------------------------------------------------
// TranslatorController (§1.3, §1.4.9)
// ---------------------------------------------------------------------------

// HardwareDiscoveryChecker abstracts the pre-flight check from §1.4.9:
// confirming a DPF DPUDiscovery resource exists for a given node before any
// provisioning object is created for it. Separated as an interface so the
// Translator's control flow can be unit tested without a live DPF API.
type HardwareDiscoveryChecker interface {
	// IsDiscovered reports whether the named node has a corresponding,
	// DMS-reachable DPUDiscovery resource.
	IsDiscovered(ctx context.Context, nodeName string) (bool, error)
}

// ServerSideApplier abstracts server-side apply. In production this would
// call client.Patch(ctx, obj, client.Apply, client.FieldOwner(...),
// client.ForceOwnership) after converting the local stand-in into the real
// generated DPF type.
type ServerSideApplier interface {
	Apply(ctx context.Context, obj *DPFChildObject, fieldManager string) error
}

// TranslatorController reconciles OPI DpuOperatorConfig objects into DPF
// child objects. It watches OPI CR spec changes only (via
// GenerationChangedPredicate, see SetupWithManager) to avoid the
// self-triggering feedback loop described in §1.4.10.
//
// B-4 audit fix: this controller no longer performs its own version
// discovery. It receives the active Translator from the
// VersionCompatibilityController via the shared ActiveTranslator field,
// avoiding a dual-discovery race where both controllers could momentarily
// disagree on which translation profile is current.
type TranslatorController struct {
	Client Client

	// ActiveTranslator is set by VersionCompatibilityController whenever a
	// valid translation profile is resolved. The TranslatorController reads
	// it but never writes it — single-writer, multi-reader.
	ActiveTranslator *ActiveTranslatorRef
	HardwareDiscovery HardwareDiscoveryChecker
	Nodes             NodeLister
	Applier           ServerSideApplier
	Recorder          EventRecorder
}

// Reconcile implements the Translator Controller's control loop. The body
// is a skeleton: guard clauses and decision points are real, but no actual
// cluster I/O is performed.
func (r *TranslatorController) Reconcile(ctx context.Context, req Request) (Result, error) {
	var cfg DpuOperatorConfig
	if err := r.Client.Get(ctx, req.NamespacedName, &cfg); err != nil {
		if IsNotFound(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("fetching DpuOperatorConfig: %w", err)
	}

	// Deletion is handled entirely by the finalizer-driven paths in
	// DeadlockRecoveryController; the Translator does not participate in
	// teardown beyond respecting the deletionTimestamp guard here.
	if cfg.ObjectMeta.DeletionTimestamp != nil {
		return Result{}, nil
	}

	// §1.4.8: read the active translation profile selected by
	// VersionCompatibilityController. If no profile is active (version
	// mismatch), the Translator exits early — the VersionCompatibility-
	// Controller is responsible for surfacing the error as an Event.
	if r.ActiveTranslator == nil {
		return Result{}, errors.New("active translator reference is not configured")
	}
	translator := r.ActiveTranslator.Get()
	if translator == nil {
		// No compatible profile currently active; the Version-Compatibility
		// Controller has already emitted the appropriate Event/condition.
		return Result{}, nil // deliberately no requeue: circuit breaker
	}

	// §1.4.9: hardware pre-flight gate, per node, before any DPUSet entry
	// is created.
	//
	// B-5 audit fix: NodeSelector is map[string]string — a label selector
	// whose keys are label names, not node names. We first resolve the
	// selector to actual node names via NodeLister, then check each node.
	if r.Nodes == nil {
		return Result{}, errors.New("node lister is not configured")
	}
	nodeNames, err := r.Nodes.ListMatchingNodes(ctx, cfg.Spec.NodeSelector)
	if err != nil {
		return Result{}, fmt.Errorf("listing nodes matching selector: %w", err)
	}
	for _, nodeName := range nodeNames {
		if r.HardwareDiscovery == nil {
			return Result{}, errors.New("hardware discovery checker is not configured")
		}
		discovered, err := r.HardwareDiscovery.IsDiscovered(ctx, nodeName)
		if err != nil {
			return Result{}, fmt.Errorf("checking hardware discovery for node %q: %w", nodeName, err)
		}
		if !discovered {
			// TODO: set PendingHardwareDiscovery condition (aggregated
			// fleet-wide count) rather than a bare requeue, and escalate to
			// HardwareDiscoveryFailed after HardwareDiscoveryTimeout.
			return Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// §1.4.3: idempotent, declarative translation — recompute the full
	// desired child-object set on every pass rather than creating once.
	desired, err := translator.Translate(ctx, &cfg.Spec)
	if err != nil {
		return Result{}, fmt.Errorf("translating OPI spec to DPF objects: %w", err)
	}

	if r.Applier == nil {
		return Result{}, errors.New("server-side applier is not configured")
	}

	owner := OwnerReference{
		APIVersion: "dpuoperator.opi.io/v1alpha1",
		Kind:       "DpuOperatorConfig",
		Name:       cfg.ObjectMeta.Name,
		UID:        cfg.ObjectMeta.UID,
	}
	for i := range desired {
		obj := desired[i]
		if obj.ObjectMeta.Labels == nil {
			obj.ObjectMeta.Labels = make(map[string]string)
		}
		obj.ObjectMeta.Labels[LabelManagedBy] = ManagedByValue
		obj.ObjectMeta.Labels[LabelSourceUID] = string(cfg.ObjectMeta.UID)
		obj.ObjectMeta.OwnerReferences = []OwnerReference{owner}

		if err := r.Applier.Apply(ctx, obj, FieldManagerAdapter); err != nil {
			return Result{}, fmt.Errorf("server-side applying %s/%s %s: %w",
				obj.ObjectMeta.Namespace, obj.ObjectMeta.Name, obj.Kind, err)
		}
	}

	return Result{}, nil
}

// SetupWithManager registers the TranslatorController with the manager,
// filtering on spec-only changes (GenerationChangedPredicate) so that
// status-only patches written by StatusAggregatorController cannot
// re-trigger this reconciler — see §1.4.10.
func (r *TranslatorController) SetupWithManager(mgr Manager) error {
	return NewControllerManagedBy(mgr).
		For(&DpuOperatorConfig{}).
		WithEventFilter(GenerationChangedPredicate{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// StatusAggregatorController (§1.3, §1.4.4, §1.4.12)
// ---------------------------------------------------------------------------

// TenantClusterProbe abstracts a status read against the DPU tenant
// cluster, allowing the ordering logic from §1.4.12 (endpoint-change check
// before reboot-tolerant staleness) to be unit tested independently of a
// live tenant cluster.
type TenantClusterProbe interface {
	// CurrentEndpoint returns the DPUCluster's currently recorded
	// control-plane endpoint.
	CurrentEndpoint(ctx context.Context, dpuClusterName string) (string, error)

	// ProbeServiceStatus attempts to read DPUService status from the
	// tenant cluster and reports success/failure without panicking on
	// unreachability.
	ProbeServiceStatus(ctx context.Context, endpoint string) (ready bool, err error)
}

// StatusAggregatorController reflects DPF status (host cluster and, via
// TenantClusterProbe, the DPU tenant cluster) onto the OPI CR's /status
// subresource. It never watches the OPI CR itself, only DPF-side objects,
// which is the second independent layer of protection against the
// self-triggering loop described in §1.4.10.
type StatusAggregatorController struct {
	Client Client

	TenantProbe TenantClusterProbe

	// C-1 audit fix: in real controller-runtime, Reconcile can be called
	// concurrently for different keys. These maps require synchronization.
	mu                  sync.Mutex
	lastKnownEndpoint   map[NamespacedName]string
	consecutiveFailures map[NamespacedName]int
	windowStart         map[NamespacedName]time.Time
}

// Reconcile implements the Status-Aggregator Controller's control loop.
func (r *StatusAggregatorController) Reconcile(ctx context.Context, req Request) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.lastKnownEndpoint == nil {
		r.lastKnownEndpoint = make(map[NamespacedName]string)
	}
	if r.consecutiveFailures == nil {
		r.consecutiveFailures = make(map[NamespacedName]int)
	}
	if r.windowStart == nil {
		r.windowStart = make(map[NamespacedName]time.Time)
	}

	// §1.4.12: check for an endpoint change BEFORE falling into reboot
	// tolerance. This ordering is the specific fix for the bug where a
	// permanent routing change was silently absorbed by logic meant for
	// temporary reboot-induced partitions.
	currentEndpoint, err := r.TenantProbe.CurrentEndpoint(ctx, req.Name)
	if err != nil {
		return Result{}, fmt.Errorf("reading DPUCluster status endpoint: %w", err)
	}
	if prev, seen := r.lastKnownEndpoint[req.NamespacedName]; seen && prev != currentEndpoint {
		// TODO: set ConditionTenantEndpointChanged, rebuild the tenant
		// client-go transport against currentEndpoint, and reset failure
		// counters before proceeding — a changed endpoint should not
		// inherit the old endpoint's failure history.
		r.consecutiveFailures[req.NamespacedName] = 0
	}
	r.lastKnownEndpoint[req.NamespacedName] = currentEndpoint

	ready, err := r.TenantProbe.ProbeServiceStatus(ctx, currentEndpoint)
	if err != nil {
		// §1.4.4: a failed observation is Unknown, never directly False.
		r.consecutiveFailures[req.NamespacedName]++
		if _, started := r.windowStart[req.NamespacedName]; !started {
			r.windowStart[req.NamespacedName] = time.Now()
		}

		windowElapsed := time.Since(r.windowStart[req.NamespacedName]) > StatusStaleProbeWindow
		if r.consecutiveFailures[req.NamespacedName] >= StatusStaleFailureThreshold && windowElapsed {
			// TODO: downgrade Unknown -> False only here, per §1.4.4.
			//
			// C-2 audit fix: reset counters after downgrade so the next
			// failure window starts fresh rather than immediately re-triggering.
			r.consecutiveFailures[req.NamespacedName] = 0
			delete(r.windowStart, req.NamespacedName)
		}
		// TODO: set ConditionStatusStale with observation age; keep status Unknown.
		return Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Successful observation resets the failure/window bookkeeping.
	r.consecutiveFailures[req.NamespacedName] = 0
	delete(r.windowStart, req.NamespacedName)

	// TODO: patch the OPI CR's /status subresource only (never the main
	// resource) with the reduced ServicesReady condition, using
	// ObservedGeneration to tie the status to the spec generation it
	// reflects. Skip the write entirely if the computed status is
	// unchanged (§1.4.10).
	_ = ready

	return Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager registers the StatusAggregatorController, watching only
// DPF-side objects — deliberately never the OPI CR itself, per §1.4.10.
//
// B-1 audit fix: the original skeleton used DpuOperatorConfig as the For
// target, which violated §1.4.10's rule that the Status-Aggregator never
// watches the OPI CR. This now correctly watches DPUSet, the primary DPF
// provisioning resource whose status changes drive OPI status aggregation.
func (r *StatusAggregatorController) SetupWithManager(mgr Manager) error {
	// In a real build this would be provisioning.dpu.nvidia.com/DPUSet.
	return NewControllerManagedBy(mgr).
		For(&DPUSet{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// VersionCompatibilityController (§1.3, §1.4.8)
// ---------------------------------------------------------------------------

// VersionCompatibilityController performs live, bidirectional API discovery
// against both the OPI and DPF CRD schemas and selects a translation
// profile from TranslatorRegistry. It is a distinct controller from
// TranslatorController so version-discovery failures can be surfaced and
// tested independently of translation logic.
type VersionCompatibilityController struct {
	Client Client

	Registry   *TranslatorRegistry
	Discoverer SchemaDiscoverer
	Recorder   EventRecorder

	// ActiveTranslator is the shared reference that TranslatorController
	// reads. This controller is the ONLY writer — see B-4 audit fix.
	ActiveTranslator *ActiveTranslatorRef

	// adapterSelf identifies the adapter's own Deployment/Pod, which is
	// what UnsupportedOPIVersion Events are attached to instead of the OPI
	// CR (see Reconcile below).
	adapterSelf Object
}

// Reconcile re-evaluates version compatibility whenever either CRD's schema
// changes. Note that when the OPI CRD schema itself is unsupported, this
// controller emits a Kubernetes Event rather than a CR status patch, since
// it cannot safely assume the shape of a status subresource whose schema it
// does not understand — see §1.4.8.
func (r *VersionCompatibilityController) Reconcile(ctx context.Context, req Request) (Result, error) {
	opiVer, err := r.Discoverer.DiscoverOPIVersion(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("discovering OPI schema version: %w", err)
	}
	dpfVer, err := r.Discoverer.DiscoverDPFVersion(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("discovering DPF schema version: %w", err)
	}

	pair := VersionPair{OPIVersion: opiVer, DPFVersion: dpfVer}
	t, err := r.Registry.Select(pair)
	if err != nil {
		// B-4: clear the shared reference so TranslatorController stops
		// translating against a stale profile.
		if r.ActiveTranslator != nil {
			r.ActiveTranslator.Clear()
		}
		if r.Recorder != nil && r.adapterSelf != nil {
			r.Recorder.Eventf(r.adapterSelf, EventTypeWarning, string(ConditionUnsupportedOPIVersion),
				"unsupported version pair: %s", pair)
		}
		return Result{}, nil // circuit breaker: no retry storm
	}

	// B-4: publish the resolved translator so TranslatorController can
	// use it without performing its own discovery.
	if r.ActiveTranslator != nil {
		r.ActiveTranslator.Set(t)
	}

	return Result{RequeueAfter: 5 * time.Minute}, nil
}

// SetupWithManager registers the VersionCompatibilityController, watching
// CustomResourceDefinition change events for both the OPI and DPF API
// groups — never the OPI CR itself.
//
// B-2 audit fix: the original skeleton used DpuOperatorConfig as the For
// target. This controller must watch CRD schema objects, not application
// CRs, so it can detect version changes in either API group.
func (r *VersionCompatibilityController) SetupWithManager(mgr Manager) error {
	// In a real build this would be apiextensionsv1.CustomResourceDefinition.
	return NewControllerManagedBy(mgr).
		For(&CRDObject{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// DriftReconciliationController (§1.3, §1.4.7)
// ---------------------------------------------------------------------------

// DriftReconciliationController periodically re-lists adapter-owned DPF
// objects and re-applies the derived desired state, self-healing
// out-of-band edits. Objects annotated AnnotationUnmanaged are skipped and
// surfaced via ConditionManualOverrideActive instead of being silently
// fought over.
type DriftReconciliationController struct {
	Client Client

	Registry   *TranslatorRegistry
	Discoverer SchemaDiscoverer
}

// Reconcile implements the periodic drift-detection loop. It is triggered
// both by a timer (see SetupWithManager) and by normal watch events on
// labeled DPF objects.
func (r *DriftReconciliationController) Reconcile(ctx context.Context, req Request) (Result, error) {
	var obj DPFChildObject
	if err := r.Client.Get(ctx, req.NamespacedName, &obj); err != nil {
		if IsNotFound(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("fetching object for drift check: %w", err)
	}

	if val, ok := obj.ObjectMeta.Annotations[AnnotationUnmanaged]; ok && val == "true" {
		// TODO: set ConditionManualOverrideActive on the owning OPI CR and
		// skip re-application for this specific object.
		return Result{RequeueAfter: DriftReconcileInterval}, nil
	}

	// TODO: recompute desired state via the same TranslatorRegistry used by
	// TranslatorController, diff against live state, and re-apply via
	// server-side apply if drift is detected.

	return Result{RequeueAfter: DriftReconcileInterval}, nil
}

// SetupWithManager registers the DriftReconciliationController, watching
// DPF child objects (labeled with LabelManagedBy) for out-of-band changes.
//
// B-3 audit fix: the original skeleton used DpuOperatorConfig as the For
// target. This controller's job is to detect drift in DPF child objects,
// so it must watch the DPF-side resources it is responsible for re-applying.
func (r *DriftReconciliationController) SetupWithManager(mgr Manager) error {
	// In a real build this would be provisioning.dpu.nvidia.com/DPUSet,
	// filtered to objects carrying the LabelManagedBy label.
	return NewControllerManagedBy(mgr).
		For(&DPUSet{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// DeadlockRecoveryController (§1.3, §1.4.5, §1.4.6)
// ---------------------------------------------------------------------------

// DPFOperatorLivenessChecker abstracts the check for whether the DPF
// Operator itself is still running (its Deployment and/or leader Lease),
// used to distinguish "DPF is slow" from "DPF is gone" in §1.4.6.
type DPFOperatorLivenessChecker interface {
	// IsDPFOperatorLive reports whether the DPF Operator's Deployment
	// and/or leader Lease is currently present and healthy.
	IsDPFOperatorLive(ctx context.Context) (bool, error)
}

// DPFChildLister abstracts listing the DPF child objects owned by a given
// OPI CR (matched via LabelSourceUID), used by DeadlockRecoveryController
// to detect stuck-Terminating children without depending on a real
// client-go List call.
type DPFChildLister interface {
	// ListStuckChildren returns the count of DPF child objects still
	// present for the given owner UID, and the oldest deletionTimestamp
	// among them (used against DeadlockTimeout).
	ListStuckChildren(ctx context.Context, ownerUID UID) (count int, oldestTerminating time.Time, err error)

	// StripFinalizers forcibly removes DPF's own finalizers from all stuck
	// child objects for the given owner UID. Only ever called after a
	// human has applied AnnotationForceCleanup — see §1.4.6.
	StripFinalizers(ctx context.Context, ownerUID UID) error
}

// DeadlockRecoveryController implements the bottom-up finalizer chain from
// §1.4.5 for the normal case, and the emergency fallback from §1.4.6 for
// the case where the DPF Operator itself has been removed. The fallback
// path is never triggered automatically — it requires a human-applied
// AnnotationForceCleanup and always emits an audited Event.
type DeadlockRecoveryController struct {
	Client Client

	Children DPFChildLister
	Liveness DPFOperatorLivenessChecker
	Recorder EventRecorder
}

// Reconcile implements the deletion-time control loop, covering both the
// healthy-DPF path (§1.4.5) and the deadlock-fallback path (§1.4.6).
func (r *DeadlockRecoveryController) Reconcile(ctx context.Context, req Request) (Result, error) {
	var cfg DpuOperatorConfig
	if err := r.Client.Get(ctx, req.NamespacedName, &cfg); err != nil {
		if IsNotFound(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("fetching DpuOperatorConfig: %w", err)
	}

	if cfg.ObjectMeta.DeletionTimestamp == nil {
		// Not being deleted: ensure our finalizer is present so we can
		// enforce the bottom-up chain later.
		// TODO: add FinalizerAdapterCleanup via a Patch if not already present.
		return Result{}, nil
	}

	// TODO: also issue Delete on any DPF child objects that still exist
	// before relying purely on Children.ListStuckChildren for a count,
	// per §1.4.5.
	childrenRemaining, oldestTerminating, err := r.Children.ListStuckChildren(ctx, cfg.ObjectMeta.UID)
	if err != nil {
		return Result{}, fmt.Errorf("listing DPF children during deletion: %w", err)
	}

	if childrenRemaining == 0 {
		// TODO: remove FinalizerAdapterCleanup, allowing the OPI CR's
		// deletion to complete.
		return Result{}, nil
	}

	// §1.4.6: only consider the deadlock-fallback path once children have
	// been Terminating longer than DeadlockTimeout.
	if time.Since(oldestTerminating) < DeadlockTimeout {
		return Result{RequeueAfter: time.Minute}, nil
	}

	live, err := r.Liveness.IsDPFOperatorLive(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("checking DPF operator liveness: %w", err)
	}
	if live {
		// DPF is merely slow, not gone. Do not offer the force-cleanup
		// path; a healthy-but-busy DPF operator must never be short-circuited.
		return Result{RequeueAfter: time.Minute}, nil
	}

	// TODO: set ConditionDPFOperatorUnresponsive on the OPI CR here.

	if val, ok := cfg.ObjectMeta.Annotations[AnnotationForceCleanup]; !ok || val != "true" {
		// Deadlock confirmed, but no human authorization yet. Remain
		// blocked; this is a deliberate, visible wait, not a silent hang.
		return Result{RequeueAfter: time.Minute}, nil
	}

	// Human has explicitly authorized the emergency path. Audit loudly.
	if r.Recorder != nil {
		r.Recorder.Eventf(&cfg, EventTypeWarning, string(ConditionDPFOperatorUnresponsive),
			"force-cleanup annotation present; stripping DPF finalizers from %d stuck child object(s)", childrenRemaining)
	}

	if err := r.Children.StripFinalizers(ctx, cfg.ObjectMeta.UID); err != nil {
		return Result{}, fmt.Errorf("stripping DPF finalizers during force-cleanup: %w", err)
	}

	// TODO: remove FinalizerAdapterCleanup from cfg now that children are
	// unblocked.

	return Result{}, nil
}

// SetupWithManager registers the DeadlockRecoveryController.
func (r *DeadlockRecoveryController) SetupWithManager(mgr Manager) error {
	return NewControllerManagedBy(mgr).
		For(&DpuOperatorConfig{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// RBAC markers (§1.4.2, §1.4.13) — C-4 audit fix
// ---------------------------------------------------------------------------
//
// In a real kubebuilder-scaffolded project, these markers would be uncommented
// and processed by controller-gen to produce the ClusterRole/Role manifests.
// They are included here as comments to document the exact RBAC surface the
// architecture specifies, and to satisfy the audit requirement that §1.4.2
// (single-writer RBAC) and §1.4.13 (least-privilege) are represented in code.
//
// +kubebuilder:rbac:groups=dpuoperator.opi.io,resources=dpuoperatorconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dpuoperator.opi.io,resources=dpuoperatorconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=provisioning.dpu.nvidia.com,resources=bfbs;dpuflavors;dpusets;dpus;dpuclusters;dpudiscoveries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=svc.dpu.nvidia.com,resources=dpuservices;dpuservicechains,verbs=get;list;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get,resourceNames=dpucluster-kubeconfig
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// ---------------------------------------------------------------------------
// Workqueue configuration notes (§1.4.11) — C-4 audit fix
// ---------------------------------------------------------------------------
//
// In a real implementation, the manager's controller options would include
// rate-limited workqueue configuration to prevent reconciliation storms at
// fleet scale (§1.4.11). Example (using controller-runtime's options):
//
//   import "sigs.k8s.io/controller-runtime/pkg/controller"
//   import "k8s.io/client-go/util/workqueue"
//
//   ctrl.NewControllerManagedBy(mgr).
//       WithOptions(controller.Options{
//           MaxConcurrentReconciles: 5,
//           RateLimiter: workqueue.NewMaxOfRateLimiter(
//               workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 5*time.Minute),
//               &workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
//           ),
//       }).
//       For(&DpuOperatorConfig{}).
//       Complete(r)
//
// Additionally, DPUSet creation deliberately fans out through DPF's own
// native rollingUpdate.maxUnavailable mechanism rather than the adapter
// re-implementing per-device throttling.

// ---------------------------------------------------------------------------
// Manager bootstrap and leader election (§1.4.1) — B-6 audit fix
// ---------------------------------------------------------------------------
//
// This is package adapter (not package main) because the file is designed
// to be a self-contained, zero-dependency skeleton. In a real project,
// the main() below would live in cmd/main.go and import this package.
// It is included here as a commented-out reference to demonstrate the
// leader-election wiring from §1.4.1.
//
// func main() {
//     mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
//         // §1.4.1: leader election — single active replica during rolling
//         // upgrades. Lease tuned for a bounded failover window.
//         LeaderElection:          true,
//         LeaderElectionID:        "opi-nvidia-adapter-leader",
//         LeaderElectionNamespace: "dpf-operator-system",
//         LeaseDuration:           durationPtr(10 * time.Second),
//         RenewDeadline:           durationPtr(8 * time.Second),
//         RetryPeriod:             durationPtr(2 * time.Second),
//     })
//     if err != nil {
//         setupLog.Error(err, "unable to create manager")
//         os.Exit(1)
//     }
//
//     // Shared state: VersionCompatibilityController writes, TranslatorController reads.
//     activeTranslator := &ActiveTranslatorRef{}
//     registry := NewTranslatorRegistry()
//     // TODO: register concrete Translator implementations for each supported
//     // (OPI version, DPF version) pair here.
//
//     if err := (&TranslatorController{
//         Client:            mgr.GetClient(),
//         ActiveTranslator:  activeTranslator,
//         HardwareDiscovery: nil, // inject real HardwareDiscoveryChecker
//         Nodes:             nil, // inject real NodeLister
//         Applier:           nil, // inject real ServerSideApplier
//         Recorder:          mgr.GetEventRecorderFor("opi-nvidia-translator"),
//     }).SetupWithManager(mgr); err != nil {
//         setupLog.Error(err, "unable to create controller", "controller", "Translator")
//         os.Exit(1)
//     }
//
//     if err := (&StatusAggregatorController{
//         Client:      mgr.GetClient(),
//         TenantProbe: nil, // inject real TenantClusterProbe
//     }).SetupWithManager(mgr); err != nil {
//         setupLog.Error(err, "unable to create controller", "controller", "StatusAggregator")
//         os.Exit(1)
//     }
//
//     if err := (&VersionCompatibilityController{
//         Client:           mgr.GetClient(),
//         Registry:         registry,
//         Discoverer:       nil, // inject real SchemaDiscoverer
//         Recorder:         mgr.GetEventRecorderFor("opi-nvidia-version-compat"),
//         ActiveTranslator: activeTranslator,
//         adapterSelf:      nil, // inject adapter's own Pod/Deployment object
//     }).SetupWithManager(mgr); err != nil {
//         setupLog.Error(err, "unable to create controller", "controller", "VersionCompatibility")
//         os.Exit(1)
//     }
//
//     if err := (&DriftReconciliationController{
//         Client:     mgr.GetClient(),
//         Registry:   registry,
//         Discoverer: nil, // inject real SchemaDiscoverer
//     }).SetupWithManager(mgr); err != nil {
//         setupLog.Error(err, "unable to create controller", "controller", "DriftReconciliation")
//         os.Exit(1)
//     }
//
//     if err := (&DeadlockRecoveryController{
//         Client:   mgr.GetClient(),
//         Children: nil, // inject real DPFChildLister
//         Liveness: nil, // inject real DPFOperatorLivenessChecker
//         Recorder: mgr.GetEventRecorderFor("opi-nvidia-deadlock-recovery"),
//     }).SetupWithManager(mgr); err != nil {
//         setupLog.Error(err, "unable to create controller", "controller", "DeadlockRecovery")
//         os.Exit(1)
//     }
//
//     setupLog.Info("starting manager")
//     if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
//         setupLog.Error(err, "manager exited with error")
//         os.Exit(1)
//     }
// }
//
// func durationPtr(d time.Duration) *time.Duration { return &d }
