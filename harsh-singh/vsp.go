package main

import (
	"context"
	"fmt"
	"strings"
)

// DpfAdapterVSP implements the OPI VSP gRPC contract by translating each call into
// DPF custom resources. It holds no hardware logic of its own.
type DpfAdapterVSP struct {
	id       DpuIdentifier
	dpuMode  bool
	dpf      DpfClient
	endpoint IpPort
}

func NewDpfAdapterVSP(id DpuIdentifier, dpuMode bool, dpf DpfClient) *DpfAdapterVSP {
	return &DpfAdapterVSP{
		id:       id,
		dpuMode:  dpuMode,
		dpf:      dpf,
		endpoint: IpPort{IP: "127.0.0.1", Port: 50051},
	}
}

// Init ensures DPF is ready and this DPU is targeted for provisioning, then returns
// fast. Provisioning finishes asynchronously; readiness is reported via Ping.
func (v *DpfAdapterVSP) Init(ctx context.Context, req InitRequest) (IpPort, error) {
	if v.dpf == nil {
		return IpPort{}, fmt.Errorf("nvidia vsp: no DPF client wired")
	}
	ready, err := v.dpf.Ready(ctx)
	if err != nil {
		return IpPort{}, fmt.Errorf("nvidia vsp: checking DPF readiness: %w", err)
	}
	if !ready {
		return IpPort{}, fmt.Errorf("nvidia vsp: DPF not ready")
	}
	flavor := DPUFlavor{Name: "opi-" + req.DpuIdentifier, NumVFs: 8, BFB: "bf-bundle"}
	if err := v.dpf.EnsureFlavor(ctx, flavor); err != nil {
		return IpPort{}, err
	}
	set := DPUSet{
		Name:         "opi-" + req.DpuIdentifier,
		FlavorRef:    flavor.Name,
		NodeSelector: map[string]string{"opi.dpu/identifier": req.DpuIdentifier},
	}
	if err := v.dpf.EnsureDPUSet(ctx, set); err != nil {
		return IpPort{}, err
	}
	return v.endpoint, nil
}

func (v *DpfAdapterVSP) GetDevices(ctx context.Context) (DeviceListResponse, error) {
	out := DeviceListResponse{Devices: map[string]Device{}}
	devs, err := v.dpf.ListDevices(ctx, v.id)
	if err != nil {
		return out, err
	}
	for _, d := range devs {
		health := "Unhealthy"
		if d.Ready {
			health = "Healthy"
		}
		out.Devices[d.ID] = Device{ID: d.ID, Health: health, NodeName: d.NodeName}
	}
	return out, nil
}

func (v *DpfAdapterVSP) SetNumVfs(ctx context.Context, count int32) (int32, error) {
	if err := v.dpf.SetVFCount(ctx, v.id, count); err != nil {
		return 0, err
	}
	return count, nil
}

// CreateNetworkFunction maps NFRequest{input,output} to two ServiceInterfaces and a
// single-hop ServiceChain, idempotent by a name derived from the endpoints.
func (v *DpfAdapterVSP) CreateNetworkFunction(ctx context.Context, req NFRequest) error {
	in := ServiceInterface{Name: nfName("in", req), Port: req.Input}
	out := ServiceInterface{Name: nfName("out", req), Port: req.Output}
	if err := v.dpf.EnsureServiceInterface(ctx, in); err != nil {
		return err
	}
	if err := v.dpf.EnsureServiceInterface(ctx, out); err != nil {
		return err
	}
	return v.dpf.EnsureServiceChain(ctx, ServiceChain{
		Name:  nfName("chain", req),
		Ports: []string{in.Name, out.Name},
	})
}

func (v *DpfAdapterVSP) DeleteNetworkFunction(ctx context.Context, req NFRequest) error {
	return v.dpf.DeleteServiceChain(ctx, nfName("chain", req))
}

func (v *DpfAdapterVSP) Ping(ctx context.Context) (PingResponse, error) {
	devs, err := v.dpf.ListDevices(ctx, v.id)
	if err != nil {
		return PingResponse{ResponderID: string(v.id)}, err
	}
	healthy := len(devs) > 0
	for _, d := range devs {
		if !d.Ready {
			healthy = false
		}
	}
	return PingResponse{Healthy: healthy, ResponderID: string(v.id)}, nil
}

func nfName(prefix string, req NFRequest) string {
	san := func(s string) string { return strings.ReplaceAll(s, "/", "-") }
	return fmt.Sprintf("opi-%s-%s-%s", prefix, san(req.Input), san(req.Output))
}

var _ VendorVSP = (*DpfAdapterVSP)(nil)
