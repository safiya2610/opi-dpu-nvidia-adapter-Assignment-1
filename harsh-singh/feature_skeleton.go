// Command feature_skeleton is a self-contained proof-of-concept for adding NVIDIA
// BlueField support to the OPI DPU Operator by driving NVIDIA's DPF as the backend.
//
// It is split across files to mirror how this would sit in a real tree:
//
//	interfaces.go  - the OPI-facing interfaces and message types (mirrors of dpu-api)
//	detector.go    - NvidiaBlueField3Detector, the platform.VendorDetector
//	vsp.go         - DpfAdapterVSP, the plugin that turns gRPC calls into DPF CRs
//	dpf_client.go  - the DpfClient boundary and a fake used by the dry-run below
//
// The mirrored types stand in for the real upstream packages so this builds without
// external modules. Run it with: go run .
package main

import (
	"context"
	"fmt"
)

func main() {
	ctx := context.Background()

	// S1: detection.
	det := NewNvidiaDetector()
	pci := PCIDevice{Address: "0000:03:00.0", VendorID: "15b3", DeviceID: "a2dc", Serial: "MT2251X00ABC"}
	isDPU, _ := det.IsDPU(pci, nil)
	id, _ := det.GetDpuIdentifier(pci)
	fmt.Printf("[S1] %s/%s on %s: isDPU=%v id=%s\n", det.GetVendorName(), det.Name(), pci.Address, isDPU, id)

	// S2: the plugin, backed by a fake DPF client, translating the gRPC contract.
	fake := &fakeDpf{}
	vsp := NewDpfAdapterVSP(id, false, fake)

	ep, _ := vsp.Init(ctx, InitRequest{DpuIdentifier: string(id)})
	fmt.Printf("[S2] Init -> endpoint %s:%d\n", ep.IP, ep.Port)

	devs, _ := vsp.GetDevices(ctx)
	fmt.Printf("[S2] GetDevices -> %v\n", devs.Devices)

	_, _ = vsp.SetNumVfs(ctx, 16)
	_ = vsp.CreateNetworkFunction(ctx, NFRequest{Input: "pf0vf0", Output: "pf0vf1"})
	pong, _ := vsp.Ping(ctx)
	fmt.Printf("[S2] Ping -> healthy=%v\n", pong.Healthy)

	fmt.Println("\nDPF resources the adapter would apply:")
	for _, c := range fake.calls {
		fmt.Println("  " + c)
	}
}
