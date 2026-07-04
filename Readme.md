# OPI DPU Operator - NVIDIA DPF Adapter

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-blue.svg)](https://golang.org)
[![Kubernetes](https://img.shields.io/badge/kubernetes-v1.26%2B-blue.svg)](https://kubernetes.io)

A production-grade, Kubernetes-native integration adapter that introduces NVIDIA DPU support into the Open Programmable Infrastructure (OPI) operator ecosystem. By utilizing a decoupled Sub-Operator / CRD Translation Architecture, this adapter wraps NVIDIA's native DOCA Platform Framework (DPF) Operator to provision hardware-level DOCA services while preserving a vendor-neutral API layer.

---

## Table of Contents
* [Repository Layout](#repository-layout)
* [Architecture Overview](#architecture-overview)
* [Key Features](#key-features)
* [Getting Started](#getting-started)
  * [Prerequisites](#prerequisites)
  * [Local Development & Setup](#local-development--setup)
  * [Running the Operator](#running-the-operator)
* [Verification Guide](#verification-guide)
* [Design Process & LLM Prompting](#design-process--llm-prompting)
* [License](#license)

---

## Repository Layout

This repository is structured to separate design documentation, implementation skeletons, and LLM prompting records:

| File | Description |
| :--- | :--- |
| [architecture_design.md](file:///d:/opi-assignment-1-2026/architecture_design.md) | Exhaustive architectural design proposal, Mermaid diagrams, API mappings, trade-off analysis, security model, and error handling. |
| [feature_skeleton.go](file:///d:/opi-assignment-1-2026/feature_skeleton.go) | Compilable, structured Go controller skeleton implementing the OPI-to-NVIDIA reconciler using `controller-runtime`. |
| [llm_transcript.json](file:///d:/opi-assignment-1-2026/llm_transcript.json) | The structured transcript of the prompt engineering sessions and LLM interactions that formed the architectural foundation. |

---

## Architecture Overview

The repository implements a CRD Translation / Sub-Operator pattern to ensure strict separation of concerns. The adapter translates a vendor-agnostic OPI Dpu Custom Resource into an NVIDIA DpfDeployment Custom Resource, leaving the actual hardware configuration to the standalone NVIDIA DPF Operator:

```
[ Cluster Admin ] 
       │
       ▼ (Apply generic OPI Dpu CRD)
┌──────────────────────────────────────┐
│       OPI DPU Operator Core          │
│  ┌────────────────────────────────┐  │
│  │   NVIDIA DPF Adapter Reconciler│  │  <-- implemented in feature_skeleton.go
│  └───────────────┬────────────────┘  │
└──────────────────┼───────────────────┘
                   ▼ (Server-Side Apply child DpfDeployment CRD)
┌──────────────────────────────────────┐
│      NVIDIA DPF Operator             │
│  ┌────────────────────────────────┐  │
│  │  DOCA Provisioning Engine      │  │
│  └───────────────┬────────────────┘  │
└──────────────────┼───────────────────┘
                   ▼ (Provision via DOCA SDK)
┌──────────────────────────────────────┐
│        NVIDIA DPU Hardware           │
└──────────────────────────────────────┘
```

For a comprehensive explanation including sequence diagrams, state machines, and trade-off analyses, see [architecture_design.md](file:///d:/opi-assignment-1-2026/architecture_design.md).

---

## Key Features

* **Strict Vendor Isolation:** Utilizes `unstructured.Unstructured` dynamically to avoid compiling vendor-specific Go APIs and SDK libraries directly into the OPI binary, preventing dependency hell.
* **Server-Side Apply (SSA):** Uses declarative patching with dedicated field ownership (`opi-nvidia-adapter`) to safely apply modifications without race conditions or resource spec collision.
* **Cascading Garbage Collection:** Sets standard Kubernetes `OwnerReferences` on translated child CRDs so that deleting the parent OPI resource automatically triggers hardware cleanup.
* **Self-Healing Loop:** Automatically watches child DPF resources and propagates status updates (such as phase transitions) back to the parent OPI CRD.

---

## Getting Started

### Prerequisites
Before running or compiling the adapter, ensure you have:
* **Go Compiler:** Go 1.22 or higher installed.
* **Kubernetes Cluster:** Access to a Kubernetes cluster (such as KinD, Minikube, or a remote environment).
* **Kubeconfig:** Active `kubeconfig` configured to target your cluster.

### Local Development & Setup

1. **Initialize the Go Module:**
   If running in a clean workspace, initialize a module and retrieve dependencies:
   ```bash
   go mod init opi-nvidia-adapter
   go mod tidy
   ```

2. **Verify Compilation:**
   Verify that the code compiles successfully by building the binary:
   ```bash
   go build -o opi-nvidia-adapter feature_skeleton.go
   ```

3. **Format and Vet Code:**
   Ensure the code matches Go standards and static analysis checks:
   ```bash
   go fmt feature_skeleton.go
   go vet feature_skeleton.go
   ```

4. **Cleanup Build Artifacts:**
   To restore the workspace to its original clean state:
   ```bash
   # Remove compiled binary
   rm -f opi-nvidia-adapter
   # Remove temporary Go module files
   rm -f go.mod go.sum
   ```

### Running the Operator

You can launch the operator locally by pointing it to your Kubernetes API server configuration:
```bash
./opi-nvidia-adapter --kubeconfig=$HOME/.kube/config --health-probe-bind-address=:8081 --metrics-bind-address=:8080
```

---

## Verification Guide

To verify the integration workflow, you can simulate the CRD application and observe the reconciliation logs:

1. **Install the Custom Resource Definitions (CRDs):**
   Install the custom resources onto your cluster (ensure OPI and NVIDIA DPF CRDs are registered):
   ```yaml
   # CRD schemas should exist in the cluster:
   # - dpus.opi.github.io
   # - dfpdeployments.dpf.nvidia.com
   ```

2. **Apply a Test OPI Dpu Resource:**
   ```yaml
   # opi-dpu.yaml
   apiVersion: opi.github.io/v1alpha1
   kind: Dpu
   metadata:
     name: test-nvidia-dpu
     namespace: default
   spec:
     image: "nvcr.io/nvidia/doca/doca-os:2.2.0"
     profile: "high-performance-networking"
   ```
   ```bash
   kubectl apply -f opi-dpu.yaml
   ```

3. **Verify the Translated Resource Creation:**
   Check if the adapter successfully created the child `DpfDeployment` with the mapped fields:
   ```bash
   kubectl get DpfDeployment test-nvidia-dpu-dpf -o yaml
   ```

4. **Monitor the Reconciliation Logs:**
   Upon applying the resources, the operator output should look similar to:
   ```
   INFO    setup   starting manager
   INFO    setup   starting reconciler
   INFO    NvidiaDPFAdapterReconciler   Successfully reconciled DPU   {"dpu": "default/test-nvidia-dpu"}
   INFO    NvidiaDPFAdapterReconciler   Applying child DpfDeployment via Server-Side Apply
   INFO    NvidiaDPFAdapterReconciler   Syncing status from DpfDeployment to OPI CRD   {"phase": "Pending"}
   ```

---

## Design Process & LLM Prompting

This architecture was designed and iterated using Large Language Models (LLMs) in cooperation with a Principal Infrastructure Engineer persona.
* The prompt sequence focused on Kubernetes Composition patterns and decoupling.
* The trade-offs of using direct Go SDK imports (resulting in bloated binaries and GPL license challenges) vs. CRD Translation (using unstructured clients) were thoroughly explored.
* The prompt and response records are preserved in [llm_transcript.json](file:///d:/opi-assignment-1-2026/llm_transcript.json).

---


