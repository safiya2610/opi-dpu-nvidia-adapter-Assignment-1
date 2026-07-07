// Skeleton for NVIDIA BlueField (DPF) support in the OPI DPU Operator.
//
// It compiles and runs on its own with `go run feature_skeleton.go`, using only the standard
// library. The types here stand in for the real ones; comments point at the
// matching code in the operator.
package main

import (
	"context"
	"fmt"
	"strings"
)

type DpuIdentifier string

type PCIDevice struct {
	Address string
	Vendor  string
	Device  string
}

type Platform interface {
	PciDevices() ([]*PCIDevice, error)
}

// VendorDetector matches the real platform.VendorDetector. A new vendor is added to the detector list at internal/platform/vendordetector.go:69.
type VendorDetector interface {
	Name() string
	IsDpuPlatform(p Platform) (bool, error)
	IsDPU(p Platform, pci PCIDevice, seen []DpuIdentifier) (bool, error)
	GetDpuIdentifier(p Platform, pci *PCIDevice) (DpuIdentifier, error)
	GetVendorName() string
	DpuPlatformName() string
}

// VendorPlugin matches plugin.VendorPlugin, the gRPC contract every VSP serves.
type VendorPlugin interface {
	Start(ctx context.Context) (string, int32, error)
	Close()
	CreateBridgePort(portName string, mac string) error
	DeleteBridgePort(portName string) error
	CreateNetworkFunction(input string, output string) error
	DeleteNetworkFunction(input string, output string) error
}

// We build DPF objects as plain maps against fixed API versions, so we never import NVIDIA's Go code.
const (
	dpfProvisioningAPI = "provisioning.dpu.nvidia.com/v1alpha1"
	dpfServiceAPI      = "svc.dpu.nvidia.com/v1alpha1"

	ownerLabelKey   = "opi.dpu/owned"
	ownerLabelValue = "true"
	fieldManager    = "opi-nvidia"

	nvidiaVendorID    = "15b3"
	nvidiaProductName = "NVIDIA BlueField"
)

type dpfObject struct {
	APIVersion string
	Kind       string
	Name       string
	Labels     map[string]string
	Spec       map[string]any
}

// newDPFObject adds our ownership label to every object, so we only touch what we created.
func newDPFObject(apiVersion, kind, name string, spec map[string]any) dpfObject {
	return dpfObject{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       name,
		Labels:     map[string]string{ownerLabelKey: ownerLabelValue},
		Spec:       spec,
	}
}

// Applier writes an object to the cluster (real one: server-side apply with our field manager).
type Applier interface {
	Apply(ctx context.Context, obj dpfObject) error
}

// Piece 1: Detector

type NvidiaBlueFieldDetector struct{}

func NewNvidiaBlueFieldDetector() *NvidiaBlueFieldDetector { return &NvidiaBlueFieldDetector{} }

func (d *NvidiaBlueFieldDetector) Name() string            { return nvidiaProductName }
func (d *NvidiaBlueFieldDetector) GetVendorName() string   { return "NVIDIA" }
func (d *NvidiaBlueFieldDetector) DpuPlatformName() string { return "nvidia-bluefield" }

func (d *NvidiaBlueFieldDetector) IsDPU(_ Platform, pci PCIDevice, _ []DpuIdentifier) (bool, error) {
	return strings.EqualFold(pci.Vendor, nvidiaVendorID) && isBlueFieldDeviceID(pci.Device), nil
}

// IsDpuPlatform is always false for NVIDIA: a BlueField never runs OPI's DPU-side daemon, because DPF puts the card in its own cluster.
func (d *NvidiaBlueFieldDetector) IsDpuPlatform(_ Platform) (bool, error) { return false, nil }

func (d *NvidiaBlueFieldDetector) GetDpuIdentifier(_ Platform, pci *PCIDevice) (DpuIdentifier, error) {
	if pci == nil {
		return "", fmt.Errorf("nil PCI device")
	}
	safe := strings.NewReplacer(":", "-", ".", "-").Replace(pci.Address)
	return DpuIdentifier("nvidia-bluefield-" + safe), nil
}

func isBlueFieldDeviceID(device string) bool {
	switch strings.ToLower(device) {
	case "a2dc", "a2d6": // a2dc = BlueField-3; a2d6 = BlueField-2 (confirm on hardware)
		return true
	default:
		return false
	}
}

// Piece 2: Cluster module (Lane 1)

// ProvisioningInput is the fleet-wide request read from OPI's CRDs, applied once per cluster rather than per card. The NVIDIA-specific values come from a ConfigMap.
type ProvisioningInput struct {
	BFBURL       string
	Flavor       string
	NodeSelector map[string]string
}

type NvidiaClusterModule struct {
	apply Applier
}

func NewNvidiaClusterModule(a Applier) *NvidiaClusterModule { return &NvidiaClusterModule{apply: a} }

func (m *NvidiaClusterModule) Reconcile(ctx context.Context, in ProvisioningInput) error {
	for _, obj := range m.translate(in) {
		if err := m.apply.Apply(ctx, obj); err != nil {
			return fmt.Errorf("apply %s/%s: %w", obj.Kind, obj.Name, err)
		}
	}
	return nil
}

// translate builds the objects purely from the input, so if it half-fails the next run repeats it cleanly. These are fleet objects with fixed names and a node selector, created once, not one set per card.
func (m *NvidiaClusterModule) translate(in ProvisioningInput) []dpfObject {
	bfb := newDPFObject(dpfProvisioningAPI, "BFB", "opi-bfb", map[string]any{
		"url": in.BFBURL,
	})
	flavor := newDPFObject(dpfProvisioningAPI, "DPUFlavor", in.Flavor, map[string]any{})
	dpuSet := newDPFObject(dpfProvisioningAPI, "DPUSet", "opi-dpuset", map[string]any{
		"dpuNodeSelector": in.NodeSelector,
		"dpuTemplate": map[string]any{
			"spec": map[string]any{
				"bfb":       map[string]any{"name": bfb.Name},
				"dpuFlavor": in.Flavor,
			},
		},
	})
	return []dpfObject{bfb, flavor, dpuSet}
}

func FoldPhase(dpuPhase string) (readyStatus string, reason string) {
	switch strings.ToLower(dpuPhase) {
	case "ready":
		return "True", "Provisioned"
	case "error", "failed":
		return "False", "Error"
	default:
		return "False", "Provisioning"
	}
}

// --- Piece 3: VSP (Lane 2) ---

type ServiceFunctionChain struct {
	Name             string
	NodeName         string
	NetworkFunctions []NetworkFunction
}

type NetworkFunction struct {
	Name  string
	Image string
}

// WrapperChart runs any image through Helm values, because DPF's DPUService takes a Helm chart, not a plain image. This chart still needs to be built and hosted.
const WrapperChart = "oci://registry.example.com/opi/nf-wrapper"

type NvidiaVSP struct {
	apply         Applier
	dpuIdentifier DpuIdentifier
}

func NewNvidiaVSP(a Applier, id DpuIdentifier) *NvidiaVSP {
	return &NvidiaVSP{apply: a, dpuIdentifier: id}
}

func (v *NvidiaVSP) Start(_ context.Context) (string, int32, error) { return "127.0.0.1", 50051, nil }
func (v *NvidiaVSP) Close()                                         {}

func (v *NvidiaVSP) CreateBridgePort(portName, mac string) error { return nil }
func (v *NvidiaVSP) DeleteBridgePort(portName string) error      { return nil }

// CreateNetworkFunction is part of the contract, but it is not what triggers NVIDIA. Its only caller is the DPU-side daemon (dpusidemanager.go:156), which a BlueField never runs. The real Lane 2 trigger is OnServiceFunctionChain below.
func (v *NvidiaVSP) CreateNetworkFunction(input, output string) error { return nil }
func (v *NvidiaVSP) DeleteNetworkFunction(input, output string) error { return nil }

func (v *NvidiaVSP) OnServiceFunctionChain(ctx context.Context, sfc ServiceFunctionChain) error {
	for _, obj := range v.translate(sfc) {
		if err := v.apply.Apply(ctx, obj); err != nil {
			return fmt.Errorf("apply %s/%s: %w", obj.Kind, obj.Name, err)
		}
	}
	return nil
}

func (v *NvidiaVSP) translate(sfc ServiceFunctionChain) []dpfObject {
	var objs []dpfObject
	for _, nf := range sfc.NetworkFunctions {
		iface := newDPFObject(dpfServiceAPI, "DPUServiceInterface", "opi-"+nf.Name, map[string]any{
			"node": sfc.NodeName,
		})
		svc := newDPFObject(dpfServiceAPI, "DPUService", "opi-"+nf.Name, map[string]any{
			"helmChart": map[string]any{
				"source": WrapperChart,
				"values": map[string]any{"image": nf.Image},
			},
			"interfaces": []any{iface.Name},
		})
		objs = append(objs, iface, svc)
	}
	chain := newDPFObject(dpfServiceAPI, "DPUServiceChain", "opi-"+sfc.Name, map[string]any{
		"node": sfc.NodeName,
	})
	objs = append(objs, chain)

	for i := range objs {
		objs[i].Labels["opi.dpu/identifier"] = string(v.dpuIdentifier)
	}
	return objs
}

type VendorClusterModule interface {
	Name() string
	Reconcile(ctx context.Context, in ProvisioningInput) error
}

var (
	_ VendorDetector      = (*NvidiaBlueFieldDetector)(nil)
	_ VendorPlugin        = (*NvidiaVSP)(nil)
	_ VendorClusterModule = (*nvidiaModuleAdapter)(nil)
)

type nvidiaModuleAdapter struct{ *NvidiaClusterModule }

func (nvidiaModuleAdapter) Name() string { return nvidiaProductName }

type logApplier struct{}

func (logApplier) Apply(_ context.Context, obj dpfObject) error {
	fmt.Printf("APPLY %-22s %-24s apiVersion=%s owner=%s fieldManager=%s\n",
		obj.Kind, obj.Name, obj.APIVersion, obj.Labels[ownerLabelKey], fieldManager)
	return nil
}

func main() {
	ctx := context.Background()
	apply := logApplier{}

	fmt.Println("== Detector ==")
	det := NewNvidiaBlueFieldDetector()
	pci := PCIDevice{Address: "0000:03:00.0", Vendor: nvidiaVendorID, Device: "a2dc"}
	isDpu, _ := det.IsDPU(nil, pci, nil)
	id, _ := det.GetDpuIdentifier(nil, &pci)
	fmt.Printf("detector=%q isDPU=%v identifier=%s\n\n", det.Name(), isDpu, id)

	fmt.Println("== Lane 1: cluster module ==")
	mod := NewNvidiaClusterModule(apply)
	_ = mod.Reconcile(ctx, ProvisioningInput{
		BFBURL:       "http://example.com/bf-bundle.bfb",
		Flavor:       "opi-default",
		NodeSelector: map[string]string{"feature.node.kubernetes.io/pci-15b3.present": "true"},
	})
	ready, reason := FoldPhase("Ready")
	fmt.Printf("DPU phase Ready -> OPI Ready=%s reason=%s\n\n", ready, reason)

	fmt.Println("== Lane 2: VSP watch handler ==")
	vsp := NewNvidiaVSP(apply, id)
	_ = vsp.OnServiceFunctionChain(ctx, ServiceFunctionChain{
		Name:     "chain-a",
		NodeName: "worker-1",
		NetworkFunctions: []NetworkFunction{
			{Name: "firewall", Image: "example.com/firewall:v1"},
		},
	})
}
