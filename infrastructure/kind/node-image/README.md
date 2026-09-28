# kind node image for nested-container environments

**Only needed inside another container** — a CI sandbox, a dev container,
a VM-in-a-VM. On an ordinary Linux host, use the stock `kindest/node`
image and ignore this directory entirely.

## The problem

Kubelet asks static control-plane pods for `oomScoreAdj: -998`. Lowering
`oom_score_adj` below the inherited `oom_score_adj_min` requires
`CAP_SYS_RESOURCE` **in the initial user namespace**. A nested container
does not have it in its bounding set and cannot be given it, so runc's
nsexec dies:

```
nsexec[...]: failed to update /proc/self/oom_score_adj: Permission denied
```

which containerd surfaces as the considerably less helpful:

```
runc create failed: unable to start container process:
can't get final child's PID from pipe: EOF
```

Every pod sandbox fails, so etcd, kube-apiserver, kube-controller-manager
and kube-scheduler never start and `kind create cluster` times out waiting
for a control plane.

## The workaround

A `runc` wrapper that strips `process.oomScoreAdj` from the OCI bundle
before exec'ing the real runc.

```sh
docker build -t ofb-kind-node:v1.29.14 infrastructure/kind/node-image
NODE_IMAGE=ofb-kind-node:v1.29.14 ../up.sh
```

`oom_score_adj` only biases the kernel's choice of OOM victim. Dropping it
changes which process would be killed under memory pressure on a throwaway
dev node and changes nothing about how Kubernetes schedules, admits or runs
workloads.

**Never use this on a real node.** On a real node the value is doing its
job: it is what stops the kernel from killing the API server before it
kills a workload pod.
