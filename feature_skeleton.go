package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	opiGroup   = "opi.github.io"
	opiVersion = "v1alpha1"
	opiKind    = "Dpu"
	fieldOwner = "opi-nvidia-adapter"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

// VendorAdapter defines the abstraction between the OPI reconciliation loop and
// a vendor-specific translation layer.
type VendorAdapter interface {
	Translate(name, namespace string, opiSpec map[string]interface{}) (*unstructured.Unstructured, error)
	OwnedGVK() schema.GroupVersionKind
}

type VendorRegistry map[string]VendorAdapter

// NvidiaTranslator implements NVIDIA-specific translation logic from OPI DPU
// resources into DPF custom resources.
type NvidiaTranslator struct{}

// VendorDPFAdapterReconciler reconciles OPI DPU resources by translating them
// into vendor-specific DPF CRs and syncing status back into the OPI API.
type VendorDPFAdapterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry VendorRegistry
}

// RBAC permissions required for the translation operator
// +kubebuilder:rbac:groups=opi.github.io,resources=dpus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opi.github.io,resources=dpus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpfdeployments,verbs=get;list;watch;create;update;patch;delete
func (r *VendorDPFAdapterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var opiDPU unstructured.Unstructured
	opiDPU.SetGroupVersionKind(schema.GroupVersionKind{Group: opiGroup, Version: opiVersion, Kind: opiKind})

	if err := r.Get(ctx, req.NamespacedName, &opiDPU); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get OPI DPU resource")
		return ctrl.Result{}, err
	}

	opiSpec, found, err := unstructured.NestedMap(opiDPU.Object, "spec")
	if err != nil {
		logger.Error(err, "failed to parse OPI DPU spec")
		return ctrl.Result{}, err
	}
	if !found {
		logger.Info("OPI DPU spec not found")
		return ctrl.Result{}, nil
	}

	vendor, _, _ := unstructured.NestedString(opiSpec, "vendor")
	adapter, ok := r.Registry[vendor]
	if !ok {
		logger.Info("Skipping unsupported vendor", "vendor", vendor)
		return ctrl.Result{}, nil
	}

	dpfCRD, err := adapter.Translate(req.Name, req.Namespace, opiSpec)
	if err != nil {
		logger.Error(err, "failed to translate OPI spec to DPF spec")
		return ctrl.Result{}, err
	}

	if err := ctrl.SetControllerReference(&opiDPU, dpfCRD, r.Scheme); err != nil {
		logger.Error(err, "failed to set controller reference")
		return ctrl.Result{}, err
	}

	if err := r.Patch(ctx, dpfCRD, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership); err != nil {
		logger.Error(err, "failed to apply DPF CR")
		return ctrl.Result{}, err
	}

	if err := r.syncStatus(ctx, &opiDPU, dpfCRD.GetName(), req.Namespace, adapter.OwnedGVK()); err != nil {
		logger.Error(err, "failed to sync status from DPF to OPI")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (t *NvidiaTranslator) Translate(name, namespace string, opiSpec map[string]interface{}) (*unstructured.Unstructured, error) {
	dpf := &unstructured.Unstructured{}
	dpf.SetGroupVersionKind(schema.GroupVersionKind{Group: "dpf.nvidia.com", Version: "v1alpha1", Kind: "DpfDeployment"})
	dpf.SetName(fmt.Sprintf("%s-dpf", name))
	dpf.SetNamespace(namespace)

	dpfSpec := make(map[string]interface{})

	if image, ok := opiSpec["image"].(string); ok && image != "" {
		dpfSpec["systemImage"] = image
	}
	if profile, ok := opiSpec["profile"].(string); ok && profile != "" {
		dpfSpec["configurationProfile"] = profile
	}
	if resources, ok := opiSpec["resources"].(map[string]interface{}); ok {
		dpfSpec["resources"] = resources
	}
	if network, ok := opiSpec["network"].(map[string]interface{}); ok {
		dpfSpec["networkConfig"] = network
	}

	if err := unstructured.SetNestedMap(dpf.Object, dpfSpec, "spec"); err != nil {
		return nil, err
	}

	return dpf, nil
}

func (t *NvidiaTranslator) OwnedGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "dpf.nvidia.com", Version: "v1alpha1", Kind: "DpfDeployment"}
}

func (r *VendorDPFAdapterReconciler) syncStatus(ctx context.Context, opiDPU *unstructured.Unstructured, dpfName, namespace string, gvk schema.GroupVersionKind) error {
	dpf := &unstructured.Unstructured{}
	dpf.SetGroupVersionKind(gvk)

	if err := r.Get(ctx, types.NamespacedName{Name: dpfName, Namespace: namespace}, dpf); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	phase, _, _ := unstructured.NestedString(dpf.Object, "status", "phase")
	if phase == "" {
		phase = "Pending"
	}

	if err := unstructured.SetNestedField(opiDPU.Object, phase, "status", "phase"); err != nil {
		return err
	}

	return r.Status().Update(ctx, opiDPU)
}

func (r *VendorDPFAdapterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opiGVK := &unstructured.Unstructured{}
	opiGVK.SetGroupVersionKind(schema.GroupVersionKind{Group: opiGroup, Version: opiVersion, Kind: opiKind})

	builder := ctrl.NewControllerManagedBy(mgr).For(opiGVK)
	for _, adapter := range r.Registry {
		dpfGVK := &unstructured.Unstructured{}
		dpfGVK.SetGroupVersionKind(adapter.OwnedGVK())
		builder = builder.Owns(dpfGVK)
	}

	return builder.Complete(r)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
		"Enabling this will ensure there is only one active controller manager.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "opi-nvidia-adapter-leader-election",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	registry := VendorRegistry{
		"nvidia": &NvidiaTranslator{},
	}

	reconciler := &VendorDPFAdapterReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Registry: registry,
	}

	if err = reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VendorDPFAdapterReconciler")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

