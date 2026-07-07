# Submission notes, Assignment 1

## Assignment chosen

Assignment 1: LLM-assisted architecture design for the OPI DPU Operator, adding NVIDIA BlueField
support while reusing the existing DPF operator.

## Deliverables in this folder

| File | What it is |
|---|---|
| `architecture_design.md` | The design: repository exploration, the proposal, diagrams, grounded code snippets, trade-offs, alternatives considered, and limitations. |
| `llm_transcript.json` | The LLM-assisted design session that produced the architecture. Valid JSON, shape `[{role, content}, ...]`. |
| `feature_skeleton.go` + `interfaces.go` + `detector.go` + `vsp.go` + `dpf_client.go` | The Go skeleton, one `main` package split across files to resemble a real tree. |
| `go.mod` | Module definition. |
| `NOTES.md` | This file. |

## What is verified vs assumed

I want to be honest about the difference, since some of this is reasoned rather than run.

Read directly from source (so: confirmed to exist as described):

- OPI `internal/platform/vendordetector.go`: the `VendorDetector` interface and the
  `NewDpuDetectorManager` registration list (Intel, Marvell, NetSec, with the "add more detectors
  here" comment).
- OPI `dpu-api/api.proto`: the VSP gRPC contract (LifeCycle, Device, NetworkFunction, Heartbeat).
- OPI `api/v1/*_types.go`: `DataProcessingUnit`, `ServiceFunctionChain`, and the rest.
- OPI `internal/daemon/*`: the daemon layout (host and DPU managers, device plugin, sfc-reconciler).
- DPF `api/{provisioning,operator,dpuservice}/v1alpha1`: the CRD kinds, plus the specific field names
  used in the code snippets (`DPUSetSpec.DPUDeviceSelector`, `DPUTemplateSpec.{BFB,DPUFlavor}`,
  `ServiceChainSpec.Switches[].Ports[].ServiceInterface.MatchLabels`).
- The BlueField-3 PCI device IDs used in `detector.go` (`a2dc` integrated ConnectX-7, `a2da`/`a2db`
  SoC), from the pci.ids database.

Assumed, not verified against running hardware or a live cluster:

- That `NFRequest{input, output}` map to DPF `ServiceInterface` ports (inferred from the proto and
  CRDs, not from a running `sfc-reconciler`).
- That DPF is co-installed and OPI can be granted RBAC on its CRDs.

## Where I stopped, and what I would do next

- I could not validate this against a real BlueField-3 or a live DPF install, so the flow is reasoned
  from source, not observed. First thing I'd do with hardware: confirm the
  `Init -> DPUSet -> DPU Ready` path before writing the service-chain code.
- The mapping from an NF container image to a DPF `DPUServiceTemplate` / `DPUService` is sketched, not
  fully specified.
- Zero-Trust-mode client wiring is designed but not implemented; the skeleton covers Host Trusted.
- Multi-vendor chains (an Intel NF followed by an NVIDIA NF in one chain) are out of scope.

Section 9 of `architecture_design.md` has the full list.

## How to run the skeleton

```bash
go run .                 # runs the dry-run
go build ./... && go vet ./...
```
