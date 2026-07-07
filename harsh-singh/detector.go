package main

import (
	"fmt"
	"strings"
)

const (
	nvidiaVendorID = "15b3" // NVIDIA/Mellanox PCI vendor id
	vendorNVIDIA   = "NVIDIA"
	productBF3     = "BlueField-3"
)

// bluefield3DeviceIDs are the BlueField-3 PCI device IDs treated as a DPU, from the
// pci.ids database (vendor 15b3). a2dc is the host-visible integrated ConnectX-7 NIC;
// a2da/a2db are the SoC functions. Plain 15b3 ConnectX NICs are excluded because they
// are not listed here.
var bluefield3DeviceIDs = map[string]bool{
	"a2dc": true, // MT43244 BlueField-3 integrated ConnectX-7 network controller
	"a2da": true, // MT43244 BlueField-3 SoC (crypto enabled)
	"a2db": true, // MT43244 BlueField-3 SoC (crypto disabled)
}

type NvidiaBlueField3Detector struct{}

func NewNvidiaDetector() *NvidiaBlueField3Detector { return &NvidiaBlueField3Detector{} }

func (d *NvidiaBlueField3Detector) Name() string            { return productBF3 }
func (d *NvidiaBlueField3Detector) GetVendorName() string   { return vendorNVIDIA }
func (d *NvidiaBlueField3Detector) DpuPlatformName() string { return "nvidia-bluefield3" }

func (d *NvidiaBlueField3Detector) IsDPU(pci PCIDevice, seen []DpuIdentifier) (bool, error) {
	if strings.ToLower(pci.VendorID) != nvidiaVendorID {
		return false, nil
	}
	if !bluefield3DeviceIDs[strings.ToLower(pci.DeviceID)] {
		return false, nil
	}
	id, err := d.GetDpuIdentifier(pci)
	if err != nil {
		return false, err
	}
	for _, s := range seen { // dedupe the second port of a dual-port DPU
		if s == id {
			return false, nil
		}
	}
	return true, nil
}

func (d *NvidiaBlueField3Detector) GetDpuIdentifier(pci PCIDevice) (DpuIdentifier, error) {
	if pci.Serial == "" {
		return "", fmt.Errorf("nvidia: empty serial for device %s", pci.Address)
	}
	return DpuIdentifier("nvidia-bf3-" + pci.Serial), nil
}

func (d *NvidiaBlueField3Detector) VspPlugin(dpuMode bool, id DpuIdentifier) (VendorVSP, error) {
	return NewDpfAdapterVSP(id, dpuMode, nil), nil
}

var _ VendorDetector = (*NvidiaBlueField3Detector)(nil)
