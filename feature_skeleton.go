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

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

// NvidiaDPFAdapterReconciler reconciles an OPI DPU object and translates it into an NVIDIA DPF object.
// It leverages unstructured.Unstructured to avoid tightly coupling vendor-specific Go types into the OPI binary.
type NvidiaDPFAdapterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// RBAC permissions required for the translation operator
// +kubebuilder:rbac:groups=opi.github.io,resources=dpus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opi.github.io,resources=dpus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpfdeployments,verbs=get;list;watch;create;update;patch;delete

func (r *NvidiaDPFAdapterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the OPI DPU instance
	var opiDPU unstructured.Unstructured
	opiDPU.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "opi.github.io",
		Version: "v1alpha1",
		Kind:    "Dpu",
	})

	if err := r.Get(ctx, req.NamespacedName, &opiDPU); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get OPI DPU resource")
		return ctrl.Result{}, err
	}

	// 2. Extract the generic Spec for Translation
	spec, found, err := unstructured.NestedMap(opiDPU.Object, "spec")
	if err != nil || !found {
		logger.Info("OPI DPU spec not found or invalid format")
		return ctrl.Result{}, nil
	}

	// 3. Translate OPI intent to NVIDIA DPF CRD
	dpfCRD, err := r.translateToDPF(req.Name, req.Namespace, spec)
	if err != nil {
		logger.Error(err, "Failed to translate OPI Spec to DPF Spec")
		return ctrl.Result{}, err
	}

	// Set OPI DPU as the owner and controller of the DPF CRD for garbage collection
	if err := ctrl.SetControllerReference(&opiDPU, dpfCRD, r.Scheme); err != nil {
		logger.Error(err, "Failed to set ControllerReference")
		return ctrl.Result{}, err
	}

	// 4. Server-Side Apply the translated DPF CRD
	if err := r.Patch(ctx, dpfCRD, client.Apply, client.FieldOwner("opi-nvidia-adapter"), client.ForceOwnership); err != nil {
		logger.Error(err, "Failed to apply DPF CRD via Server-Side Apply")
		return ctrl.Result{}, err
	}

	// 5. Sync Status from DPF to OPI CRD
	if err := r.syncStatus(ctx, &opiDPU, dpfCRD.GetName(), req.Namespace); err != nil {
		logger.Error(err, "Failed to sync status from DPF to OPI CRD")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NvidiaDPFAdapterReconciler) translateToDPF(name, namespace string, opiSpec map[string]interface{}) (*unstructured.Unstructured, error) {
	dpf := &unstructured.Unstructured{}
	dpf.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "dpf.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DpfDeployment",
	})
	dpf.SetName(fmt.Sprintf("%s-dpf", name))
	dpf.SetNamespace(namespace)

	dpfSpec := make(map[string]interface{})
	if image, ok := opiSpec["image"].(string); ok {
		dpfSpec["systemImage"] = image
	}
	if profile, ok := opiSpec["profile"].(string); ok {
		dpfSpec["configurationProfile"] = profile
	}

	if err := unstructured.SetNestedMap(dpf.Object, dpfSpec, "spec"); err != nil {
		return nil, err
	}

	return dpf, nil
}

func (r *NvidiaDPFAdapterReconciler) syncStatus(ctx context.Context, opiDPU *unstructured.Unstructured, dpfName, namespace string) error {
	dpf := &unstructured.Unstructured{}
	dpf.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "dpf.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DpfDeployment",
	})

	err := r.Get(ctx, types.NamespacedName{Name: dpfName, Namespace: namespace}, dpf)
	if err != nil {
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

func (r *NvidiaDPFAdapterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opiGVK := &unstructured.Unstructured{}
	opiGVK.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "opi.github.io",
		Version: "v1alpha1",
		Kind:    "Dpu",
	})
	
	dpfGVK := &unstructured.Unstructured{}
	dpfGVK.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "dpf.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DpfDeployment",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(opiGVK).
		Owns(dpfGVK).
		Complete(r)
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
	
	opts := zap.Options{
		Development: true,
	}
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

	if err = (&NvidiaDPFAdapterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NvidiaDPFAdapterReconciler")
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
