package main

import "context"

// Mirrors of the upstream OPI types the plugin talks to. In the real dpu-operator
// these come from api/dpu-api (generated gRPC) and internal/platform; they are
// duplicated here only so the skeleton builds with no external modules.

type PCIDevice struct {
	Address  string
	VendorID string
	DeviceID string
	Serial   string
}

type DpuIdentifier string

type Device struct {
	ID       string
	Health   string
	NodeName string
}

type DeviceListResponse struct {
	Devices map[string]Device
}

type NFRequest struct {
	Input  string
	Output string
}

type InitRequest struct {
	DpuMode       bool
	DpuIdentifier string
}

type IpPort struct {
	IP   string
	Port int32
}

type PingResponse struct {
	Healthy     bool
	ResponderID string
}

// VendorVSP is the Go shape of the four gRPC services in dpu-api/api.proto.
type VendorVSP interface {
	Init(ctx context.Context, req InitRequest) (IpPort, error)
	GetDevices(ctx context.Context) (DeviceListResponse, error)
	SetNumVfs(ctx context.Context, count int32) (int32, error)
	CreateNetworkFunction(ctx context.Context, req NFRequest) error
	DeleteNetworkFunction(ctx context.Context, req NFRequest) error
	Ping(ctx context.Context) (PingResponse, error)
}

// VendorDetector mirrors internal/platform.VendorDetector, trimmed to the methods
// that matter here.
type VendorDetector interface {
	Name() string
	GetVendorName() string
	DpuPlatformName() string
	IsDPU(pci PCIDevice, seen []DpuIdentifier) (bool, error)
	GetDpuIdentifier(pci PCIDevice) (DpuIdentifier, error)
	VspPlugin(dpuMode bool, id DpuIdentifier) (VendorVSP, error)
}
