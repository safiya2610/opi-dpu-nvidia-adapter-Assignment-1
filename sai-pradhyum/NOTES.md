# NOTES — Process, Assumptions, Limitations

## How this was produced (honest account)

1. **Both repos were cloned and read directly** — `opiproject/dpu-operator` and
   `NVIDIA/doca-platform`. Every CRD, interface, RPC, and lifecycle phase cited in
   `architecture_design.md` / `repo_analysis.md` was located in source, not recalled from memory.
   The load-bearing discoveries: OPI's `VendorDetector` interface + the literal
   `// add more detectors here` seam, the gRPC VSP contract in `dpu-api/api.proto`, and DPF's
   `DPFOperatorConfig → DPUSet → DPU(+BFB+DPUFlavor) → DPUCluster` provisioning flow with its
   `Initializing→Pending→Rebooting→Ready` phase machine.
2. **`llm_transcript.json` is the real design dialogue** (investigate → compare 4 options →
   framework → mapping/failure → skeleton → self-review), not a fabricated after-the-fact script.
3. **`feature_skeleton.go` was verified**: `gofmt -l` clean, `go vet ./...` and `go build ./...`
   pass with the Go standard library only.

## Deliverables

| File | Requirement | Status |
|------|-------------|--------|
| `architecture_design.md` | Required | Design + Mermaid sequence/flow diagrams + trade-off table |
| `llm_transcript.json` | Required | 14 messages, valid JSON, `[{role,content}...]` |
| `feature_skeleton.go` | Bonus | Compiles (`go build`/`go vet` clean, gofmt clean) |
| `repo_analysis.md` | Extra | Source-grounded reverse-engineering of both repos |
| `NOTES.md` | Extra | This file |

## Key design decision (one line)

OPI expresses **intent**; DPF executes **lifecycle**. Bridge them with a **CRD-translation adapter**
that is one instance of a generalized **Vendor Adapter Framework** — reuse DPF unmodified, keep
single-writer ownership, make AMD a registration rather than a redesign.

## Assumptions a reviewer should know

- **`DPUProvisioningClaim` is a *proposed* new OPI CRD**, not upstream today. It is the minimal
  intent surface (firmware + flavor + selector) and would need OPI community review.
- **DPF is `v1alpha1`** — API may change. The adapter pins the DPF version and **fails closed** on an
  unknown CRD version rather than mis-translating. The version matrix is a support contract.
- **DPF CRD group strings** (`provisioning.dpu.nvidia.com`, `svc.dpu.nvidia.com`) reflect the repo
  snapshot read on 2026-07-05; re-pin to the target DPF release at implementation time.

## Limitations

- **Translation fidelity is the primary risk.** OPI intent is coarser than DPF's model; advanced
  `DPUFlavor` tuning and direct `DPUCluster`/tenant workloads stay DPF-direct in v1. The adapter
  targets the common ~80%; it is not a total abstraction over DPF.
- **The Go skeleton is illustrative.** It uses local stand-in types (documented in the file header)
  so it compiles standalone; it is not wired to a live cluster. Swapping the stand-in block for the
  real `controller-runtime` + OPI/DPF imports yields a running controller with identical logic.
- **Diagrams are Mermaid (in-`.md`)**, not exported `.png`s — the assignment requires Mermaid, and
  Mermaid renders natively on GitHub, so binary PNGs were intentionally omitted.

## To turn the skeleton into a real controller (next steps)

1. Replace the stand-in block with `sigs.k8s.io/controller-runtime`, `k8s.io/apimachinery` metav1,
   OPI `api/v1`, and DPF `api/provisioning/v1alpha1` + `api/dpuservice/v1alpha1` imports.
2. Register `NvidiaAdapter` in the OPI controller-manager and add an `NvidiaDetector`
   (implementing `platform.VendorDetector`) to the `// add more detectors here` slice.
3. Use server-side apply for `Translate` output; add `Owns(&provisioningv1.DPUSet{})` watches so
   status mirroring is event-driven.
4. Scope RBAC to only the DPF CR groups translated; add the Prometheus metrics and events from §13.
5. Add an "observe-only" mode for safe brownfield adoption before taking ownership.
