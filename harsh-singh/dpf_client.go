package main

import (
	"context"
	"fmt"
)

// Minimal mirrors of the DPF CRDs the adapter builds. The real types live in
// github.com/nvidia/doca-platform/api/{provisioning,dpuservice}/v1alpha1.

type DPUFlavor struct {
	Name   string
	NumVFs int32
	BFB    string
}

type DPUSet struct {
	Name         string
	FlavorRef    string
	NodeSelector map[string]string
}

type ServiceInterface struct {
	Name string
	Port string
}

type ServiceChain struct {
	Name  string
	Ports []string
}

type DPUDevice struct {
	ID       string
	NodeName string
	Ready    bool
}

// DpfClient is the boundary onto DPF. In production it wraps a controller-runtime
// client.Client; here it is an interface so the adapter can run against a fake.
// Every method is idempotent and only touches OPI-owned objects.
type DpfClient interface {
	Ready(ctx context.Context) (bool, error)
	EnsureFlavor(ctx context.Context, f DPUFlavor) error
	EnsureDPUSet(ctx context.Context, s DPUSet) error
	SetVFCount(ctx context.Context, id DpuIdentifier, n int32) error
	ListDevices(ctx context.Context, id DpuIdentifier) ([]DPUDevice, error)
	EnsureServiceInterface(ctx context.Context, si ServiceInterface) error
	EnsureServiceChain(ctx context.Context, sc ServiceChain) error
	DeleteServiceChain(ctx context.Context, name string) error
}

// fakeDpf records the calls it receives so the dry-run in main can print what the
// adapter would apply to DPF.
type fakeDpf struct{ calls []string }

func (f *fakeDpf) log(s string)                        { f.calls = append(f.calls, s) }
func (f *fakeDpf) Ready(context.Context) (bool, error) { f.log("DPF.Ready -> true"); return true, nil }

func (f *fakeDpf) EnsureFlavor(_ context.Context, x DPUFlavor) error {
	f.log("apply DPUFlavor/" + x.Name)
	return nil
}

func (f *fakeDpf) EnsureDPUSet(_ context.Context, x DPUSet) error {
	f.log("apply DPUSet/" + x.Name)
	return nil
}

func (f *fakeDpf) SetVFCount(_ context.Context, _ DpuIdentifier, n int32) error {
	f.log(fmt.Sprintf("patch numVfs=%d", n))
	return nil
}

func (f *fakeDpf) ListDevices(context.Context, DpuIdentifier) ([]DPUDevice, error) {
	f.log("list DPUDevice")
	return []DPUDevice{{ID: "bf3-0", NodeName: "worker-1", Ready: true}}, nil
}

func (f *fakeDpf) EnsureServiceInterface(_ context.Context, x ServiceInterface) error {
	f.log("apply ServiceInterface/" + x.Name)
	return nil
}

func (f *fakeDpf) EnsureServiceChain(_ context.Context, x ServiceChain) error {
	f.log("apply ServiceChain/" + x.Name)
	return nil
}

func (f *fakeDpf) DeleteServiceChain(_ context.Context, n string) error {
	f.log("delete ServiceChain/" + n)
	return nil
}
