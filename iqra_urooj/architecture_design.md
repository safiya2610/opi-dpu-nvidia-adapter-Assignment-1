# Architecture Proposal: NVIDIA Support for OPI DPU Operator via CRD Translation Layer

## 1. Executive Summary
This document outlines the architectural design to extend the OPI (Open Programmable Infrastructure) DPU Operator to support NVIDIA DPUs. Instead of rewriting hardware-specific logic, the proposed solution implements a **CRD (Custom Resource Definition) Translation Layer** utilizing an **Adapter Pattern**. 

This allows the OPI Operator to remain vendor-neutral while dynamically translating high-level, standardized OPI CRDs into the specialized Custom Resources managed by the standalone NVIDIA DPF (DOCA Platform Framework) Operator.

---

## 2. Architectural Design & Integration Pattern

The integration uses a specialized **NVIDIA Adapter Reconciliation Loop** embedded inside (or acting as a sub-component of) the main OPI Operator.

### Core Components:
*   **OPI DPU CRD:** The user-facing, vendor-agnostic declarative interface defining the desired state of the DPU infrastructure.
*   **OPI NVIDIA Adapter Controller:** Watches for OPI DPU CRDs, detects if the underlying hardware is NVIDIA, translates the OPI spec into an NVIDIA DPF spec, and applies it to the cluster.
*   **NVIDIA DPF Operator:** The native, vendor-provided operator that watches for DPF-specific CRDs and performs the low-level hardware provisioning (DOCA configuration).

---

## 3. Sequence Diagram

The following Mermaid.js diagram illustrates how a user request triggers a cascade of declarative updates across the operators to provision an NVIDIA DPU.

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Cluster Administrator
    participant OPI as OPI DPU Operator (Adapter Layer)
    participant K8s as Kubernetes API Server
    participant DPF as NVIDIA DPF Operator
    participant HW as NVIDIA BlueField DPU

    Admin->>K8s: Apply OPI DPU CRD (spec: vendor=nvidia)
    K8s->>OPI: Watch Event (New OPI DPU Object)
    
    Note over OPI: Reconcile Loop Triggers:<br/>Detects NVIDIA vendor spec
    OPI->>OPI: Translate OPI CRD to NVIDIA DPF CRD format
    
    OPI->>K8s: Create/Update NVIDIA DPF CRD object
    K8s->>DPF: Watch Event (New DPF CRD Object)
    
    Note over DPF: Reconcile Loop Triggers:<br/>Executes low-level provisioning
    DPF->>HW: Apply DOCA Platform configurations
    HW-->>DPF: Configuration Successful
    
    DPF->>K8s: Update DPF CRD Status (Status=Ready)
    K8s->>OPI: Watch Event (DPF Status Update)
    
    Note over OPI: Update parent status
    OPI->>K8s: Update OPI DPU CRD Status (Status=Ready)
    K8s-->>Admin: DPU Infrastructure Ready
