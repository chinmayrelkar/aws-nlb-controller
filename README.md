# AWS NLB Controller

The AWS NLB Controller is a Kubernetes operator designed to manage and synchronize AWS Network Load Balancers (NLBs) with Kubernetes services. It automates the process of creating, updating, and deleting NLBs based on Kubernetes service configurations.

## Description

The AWS NLB Controller extends Kubernetes functionality by providing seamless integration with AWS Network Load Balancers. It watches for changes in Kubernetes services and automatically provisions or updates corresponding NLBs in AWS. This controller simplifies the management of network load balancing for applications running in Kubernetes clusters on AWS infrastructure.

Key features include:
- Automatic creation and configuration of AWS NLBs based on Kubernetes service annotations
- Dynamic updates to NLB settings when Kubernetes services change
- Cleanup of AWS resources when Kubernetes services are deleted
- Support for multiple NLBs across different VPCs

## Getting Started

You'll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

## Before you move further

1. Update `./config/manager/manager.yaml:103` with your VPC ID
2. Update `./config/manager/manager.yaml:105` with your NLB names and NLB hosts
3. Update `./config/rbac/service_account.yaml:12` with the NLB controller IAM role 

### Running on the cluster

1. Install Instances of Custom Resources:
