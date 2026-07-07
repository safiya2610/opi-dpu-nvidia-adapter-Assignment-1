# Repository Investigation — OPI DPU Operator & NVIDIA DPF

> Source of truth: both repositories were cloned and read directly (not summarized from
> memory or blog posts). Every type / interface / RPC named below was located in source.
>
> - OPI DPU Operator — `https://github.com/opiproject/dpu-operator` (module `github.com/openshift/dpu-operator`)
> - NVIDIA DPF (DOCA Platform Framework) — `https://github.com/NVIDIA/doca-platform`

---

## Part 1 — OPI DPU Operator

### 1.1 CRDs (`api/v1/`)

| Kind | Scope | Purpose |
|------|-------|---------|
| `DpuOperatorConfig` | **Cluster** | Top-level install/enable switch for the operator. `Spec.LogLevel`; `Status.Conditions`. Singleton (`DpuOperatorConfigNamespacedName`). |
| `DataProcessingUnit` | Namespaced | Represents one detected physical DPU. `Spec.DpuProductName`, `Spec.IsDpuSide`, `Spec.NodeName`; `Status.Conditions` (`Ready`). |
| `DataProcessingUnitConfig` | Namespaced | Desired configuration applied to DPUs. |
| `ServiceFunctionChain` (`sfc`) | Namespaced | Ordered list of `NetworkFunction{Name,Image}` pods to wire into the dataplane. `Spec.NodeSelector`, `Spec.NetworkFunctions`. |

Observed status pattern: every CRD uses `[]metav1.Condition` with a `Ready` condition type
(`plugin.ReadyConditionType = "Ready"`). No custom finalizer strings were found in `api/v1`;
lifecycle/cleanup is handled inside the controllers and the node daemon.

### 1.2 Controllers (`internal/controller/`)

- `DpuOperatorConfigReconciler` — reconciles the singleton config; rolls out the daemon + CNI.
- `DataProcessingUnitReconciler` — reconciles per-DPU CRs.
- `DataProcessingUnitConfigReconciler` — reconciles desired DPU configuration.
- `ServiceFunctionChainReconciler` — reconciles SFC → network-function pods.

All are standard `controller-runtime` reconcilers: `func (r *XReconciler) Reconcile(ctx, req) (ctrl.Result, error)`.

### 1.3 The vendor extension mechanism — **this is the key finding**

OPI abstracts vendors at **two** layers:

**(a) Control-plane detection — `VendorDetector` Go interface** (`internal/platform/vendordetector.go`)

```go
type VendorDetector interface {
    Name() string
    IsDpuPlatform(platform Platform) (bool, error)
    VspPlugin(dpuMode bool, imageManager images.ImageManager, client client.Client,
              pm utils.PathManager, dpuIdentifier plugin.DpuIdentifier) (*plugin.GrpcPlugin, error)
    IsDPU(platform Platform, pci ghw.PCIDevice, dpuDevices []plugin.DpuIdentifier) (bool, error)
    GetDpuIdentifier(platform Platform, pci *ghw.PCIDevice) (plugin.DpuIdentifier, error)
    GetVendorName() string
    DpuPlatformName() string
    DpuPlatformIdentifier(platform Platform) (plugin.DpuIdentifier, error)
}
```

Detectors are registered in a slice with a literal extension comment:

```go
detectors: []VendorDetector{
    NewIntelDetector(),
    NewMarvellDetector(),
    NewNetsecAcceleratorDetector(),
    // add more detectors here     <-- the documented plug-in point
}
```

`DpuDetectorManager.DetectAll()` walks the detectors, and for a matched device it (1) builds a
`DataProcessingUnit` CR and (2) instantiates that vendor's **VSP** via `detector.VspPlugin(...)`.

**(b) Dataplane control — the VSP (Vendor Specific Plugin) gRPC contract**

A VSP is an **out-of-process gRPC server** that runs as a per-vendor container. The operator's
daemon talks to it over a unix socket (`pathManager.VendorPluginSocket()`). The Go client wrapper
is `plugin.GrpcPlugin`, and the vendor-facing contract is defined in `dpu-api/api.proto`:

```proto
service LifeCycleService     { rpc Init(InitRequest) returns (IpPort); }
service NetworkFunctionService{ rpc CreateNetworkFunction(NFRequest) returns (Empty);
                                rpc DeleteNetworkFunction(NFRequest) returns (Empty); }
service DeviceService        { rpc GetDevices(Empty) returns (DeviceListResponse);
                                rpc SetNumVfs(VfCount) returns (VfCount); }
service HeartbeatService     { rpc Ping(PingRequest) returns (PingResponse); }
```

Plus OPI standard `BridgePortService` (from `opiproject/opi-api`) for EVPN bridge ports.

**Interpretation:** A new vendor is added by (1) implementing `VendorDetector` and registering it,
and (2) shipping a VSP container that implements the gRPC contract. Intel and Marvell each do
exactly this (`internal/daemon/vendor-specific-plugins/marvell/...`, plus the Intel VSP Dockerfiles).
A `mock-vsp` exists for testing — proof the boundary is a clean, swappable seam.

### 1.4 How Intel & Marvell plug in today

| Concern | Intel | Marvell |
|---------|-------|---------|
| Detection | `NewIntelDetector()` | `NewMarvellDetector()` |
| VSP image | `Dockerfile.IntelVSP`, `Dockerfile.IntelP4`, `Dockerfile.IntelNetSecVSP` | `Dockerfile.mrvlVSP`, `Dockerfile.mrvlCPAgent` |
| Dataplane | gRPC VSP (P4 / OVS) | gRPC VSP (OVS dataplane, control-plane agent) |

**Takeaway:** OPI is *already* a vendor framework. It does **provisioning-light** work (detect,
represent as a `DataProcessingUnit`, wire SFCs) and delegates vendor specifics to a swappable
gRPC plugin. It does **not** itself flash firmware or manage a tenant Kubernetes control plane.

---

## Part 2 — NVIDIA DPF (DOCA Platform Framework)

### 2.1 API groups (`api/`)

`operator`, `provisioning`, `dpuservice`, `storage`, `vpc`, `noderesources` — each `v1alpha1`.
DPF is a **large, full-lifecycle** platform, far heavier than a single VSP.

### 2.2 Key CRDs

**Operator group** — `DPFOperatorConfig`: the top-level install/config object (chooses CNI —
Flannel/OVN/Multus, NVIPAM, SR-IOV device plugin, provisioning + dpuservice controller settings,
Kamaji/static cluster manager, monitoring/OTel, SPIFFE security …).

**Provisioning group** (`api/provisioning/v1alpha1`) — firmware + hardware lifecycle:

| Kind | Role |
|------|------|
| `BFB` | BlueField Bitstream / boot image (the firmware artifact to flash). |
| `DPUFlavor` | Hardware profile: GRUB, OVS config, sysctls, DPU mode. |
| `DPUSet` | Desired-set controller: template + node/device selectors → creates `DPU` CRs (rolling update strategy). *Analogous to a Deployment for DPUs.* |
| `DPU` | One physical BlueField instance; references a `BFB` + `DPUFlavor` + `DPUNode`/`DPUDevice`. |
| `DPUNode`, `DPUDevice` | Node the DPU lives on / the discovered PCI device. |
| `DPUCluster` | The tenant Kubernetes cluster hosted on the DPUs (Kamaji-based). |
| `DPUDeployment` | High-level bundle tying provisioning + services together. |
| `DPUDiscovery`, `DPUNodeMaintenance`, `BlueFieldSoftware` | Discovery, drain/maintenance, SW versions. |

**DPU-service group** (`api/dpuservice/v1alpha1`) — workloads that run *on* the DPU:
`DPUService`, `DPUServiceChain`, `DPUServiceInterface`, `DPUServiceIPAM`,
`DPUServiceConfiguration`, `DPUServiceCredentialRequest`, `DPUServiceNAD`.

### 2.3 Provisioning / lifecycle flow

```
DPFOperatorConfig (install)
   └─ DPUSet (template + selectors)
        └─ creates DPU CRs
             ├─ references BFB (firmware)  + DPUFlavor (hw profile)
             ├─ flashes BlueField, reboots, installs DOCA
             └─ joins the DPUCluster (Kamaji tenant control plane)
   DPUService / DPUServiceChain / DPUServiceInterface  → dataplane workloads on the DPU
```

`DPU` lifecycle phases (`api/provisioning/v1alpha1/dpu_types.go`):
`Initializing → Pending → Rebooting → Ready` (plus `Error`, `Deleting`, `DPUWarmReboot`).
`DPUSet.Status.DPUStatistics` aggregates a `map[DPUPhase]int`.
Fine-grained conditions include `BFBReady`, `BlueFieldSoftwareReady`, `DPUClusterReady`,
`DPUServiceInterfacesReady`, `DPUServiceChainsReady`.

### 2.4 Controllers

Standard `controller-runtime` reconcilers under `internal/provisioning/controllers/`
(`dpuset_controller`, `dpu_controller`, `dpucluster_controller`, `bluefieldsoftware_controller`,
`dpunode_controller`, `dpunodemaintenance_controller`, `discovery_controller`, …) and a parallel
set for `dpuservice`. DPF is self-contained: it owns the whole flash→boot→cluster-join→service flow.

---

## Part 3 — The integration gap (why this is non-trivial)

| Dimension | OPI DPU Operator | NVIDIA DPF |
|-----------|------------------|------------|
| Altitude | Detect + represent + SFC wiring; delegates specifics to a **gRPC VSP** | **Full lifecycle**: firmware flash, boot, DOCA, tenant K8s cluster, services |
| Vendor seam | `VendorDetector` interface + VSP gRPC contract | N/A — it *is* the vendor stack |
| Entry CRD | `DpuOperatorConfig` (cluster singleton) | `DPFOperatorConfig` |
| DPU object | `DataProcessingUnit` (thin) | `DPU` (rich, phase machine) + `DPUSet` |
| Firmware | not modeled | `BFB` + `DPUFlavor` |
| Dataplane wiring | `ServiceFunctionChain` | `DPUServiceChain` / `DPUServiceInterface` |
| Tenant cluster | not modeled | `DPUCluster` (Kamaji) |

**Conclusion that drives the architecture:** OPI's `DataProcessingUnit` / `ServiceFunctionChain`
map *conceptually* onto DPF's `DPU`/`DPUSet` and `DPUServiceChain`. DPF already implements everything
OPI would otherwise have to reinvent for BlueField (flashing, reboot, DOCA, tenant cluster). Therefore
the integration must **reuse DPF wholesale** and connect at the **CRD/control-plane boundary**, not
re-implement provisioning behind a VSP. The gRPC VSP seam is the right place for *dataplane* parity,
but firmware + cluster lifecycle belong to DPF's CRDs. This asymmetry is the core design problem.
