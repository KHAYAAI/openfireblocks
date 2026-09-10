#!/usr/bin/env python3
"""Asserts properties of the rendered chart that schema validation cannot.

kubeconform proves every object is well-formed and would be accepted by the
API server. Every bug below rendered perfectly valid YAML and was accepted
by the cluster -- and then broke the platform at runtime. They were found by
turning mTLS on and spreading the parties across nodes, one at a time, each
one hidden behind the last.

The expensive property of these bugs is that they are invisible until the
feature is switched on, and switching mTLS on is exactly the thing a
deployment does once, in production, under time pressure. So they are pinned
here instead.

    python3 infrastructure/helm/test-chart-invariants.py
"""

import subprocess
import sys

CHART = "infrastructure/helm/openfireblocks"
NS = "openfireblocks"

failures: list[str] = []
checks = 0


def render(**sets: str) -> list[dict]:
    args = ["helm", "template", "ci", CHART, "-n", NS]
    for k, v in sets.items():
        args += ["--set", f"{k.replace('__', '.')}={v}"]
    out = subprocess.run(args, capture_output=True, text=True)
    if out.returncode != 0:
        print(out.stderr, file=sys.stderr)
        raise SystemExit(f"helm template failed for {sets}")
    import yaml

    return [d for d in yaml.safe_load_all(out.stdout) if d]


def check(name: str, condition: bool, detail: str = "") -> None:
    global checks
    checks += 1
    if condition:
        print(f"  ok   {name}")
    else:
        print(f"  FAIL {name}" + (f"\n       {detail}" if detail else ""))
        failures.append(name)


def deployments(docs: list[dict]) -> dict[str, dict]:
    return {
        d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"
    }


def env_of(container: dict) -> dict[str, str]:
    return {e["name"]: e.get("value", "") for e in container.get("env", [])}


def container_named(pod: dict, name: str, key: str = "containers") -> dict | None:
    for c in pod.get(key, []):
        if c["name"] == name:
            return c
    return None


MTLS_ON = dict(
    mpcParty__mtls__enabled="true",
    mpcParty__mtls__autoIssue__enabled="true",
    temporalWorker__mtls__enabled="true",
    temporalWorker__mtls__autoIssue__enabled="true",
)


def main() -> int:
    print("mTLS enabled:")
    docs = render(**MTLS_ON)
    deps = deployments(docs)
    party = deps["party-1"]
    pod = party["spec"]["template"]["spec"]
    mpc = container_named(pod, "mpc-party")

    # 1. Enabling mTLS made every party pod permanently unrunnable: the
    #    probes were plain httpGet against a port that had become HTTPS with
    #    RequireAndVerifyClientCert. kubelet presents no client certificate,
    #    so every liveness probe failed the handshake and killed the
    #    container -- forever. A security control that guarantees an outage
    #    does not get switched on.
    mtls_port = mpc["ports"][0]["name"]
    health = container_named(pod, "mpc-party")
    health_ports = [p["name"] for p in health["ports"]]
    check(
        "a health port exists separately from the mTLS port",
        "health" in health_ports,
        f"container ports are {health_ports}",
    )
    for probe in ("livenessProbe", "readinessProbe"):
        target = mpc[probe]["httpGet"]["port"]
        check(
            f"{probe} does not target the mTLS port",
            target != mtls_port and target == "health",
            f"{probe} targets {target!r}; kubelet cannot complete an mTLS handshake",
        )
    check(
        "the health port is plaintext-only via HEALTH_PORT",
        "HEALTH_PORT" in env_of(mpc),
    )

    # 2. With mTLS on, the orchestrator still dialled the parties over plain
    #    HTTP, so every DKG failed with "Client sent an HTTP request to an
    #    HTTPS server".
    gw = container_named(
        deps["ci-openfireblocks-api-gateway"]["spec"]["template"]["spec"],
        "api-gateway",
    )
    tmpl = env_of(gw)["MPC_PARTY_ENDPOINT_TEMPLATE"]
    check(
        "the party endpoint template is https when mTLS is on",
        tmpl.startswith("https://"),
        f"template is {tmpl!r}",
    )

    # 3. ...and the certificates would not have verified even then: they were
    #    issued for the service identity (party-N.internal) while peers dial
    #    the Kubernetes Service name, and TLS verifies the name dialled.
    check(
        "the endpoint template uses the fully qualified Service name",
        ".svc.cluster.local" in tmpl,
        f"template is {tmpl!r}; the short name is not in the certificate's SANs",
    )
    init = container_named(pod, "vault-pki-init", key="initContainers")
    check("the party has a cert-issuing init container", init is not None)
    if init:
        ienv = env_of(init)
        check(
            "the issued certificate carries the in-cluster DNS name as a SAN",
            ".svc.cluster.local" in ienv.get("ALT_NAMES", ""),
            f"ALT_NAMES is {ienv.get('ALT_NAMES')!r}",
        )
        check(
            "the certificate's CN is still the service identity",
            ienv.get("COMMON_NAME", "").endswith(".internal"),
            f"COMMON_NAME is {ienv.get('COMMON_NAME')!r}",
        )
        host = tmpl.split("//", 1)[1].split(":")[0].replace("{id}", "1")
        check(
            "the name peers dial is covered by the certificate",
            host in ienv.get("ALT_NAMES", "").split(","),
            f"dialling {host!r}, SANs are {ienv.get('ALT_NAMES')!r}",
        )

    # 5. Certificates were issued once at startup and nothing renewed them,
    #    so the certificate's expiry silently became the pod's lifetime.
    for dep_name, main_c in (
        ("party-1", "mpc-party"),
        ("ci-openfireblocks-temporal-worker", "temporal-worker"),
    ):
        p = deps[dep_name]["spec"]["template"]["spec"]
        renew = container_named(p, "vault-pki-renew")
        check(
            f"{dep_name} runs a certificate renewal sidecar",
            renew is not None,
            "issuance without renewal makes the certificate's expiry the pod's lifetime",
        )
        if renew:
            check(
                f"{dep_name}'s renewal sidecar is in renew mode",
                env_of(renew).get("RENEW") == "true",
            )
        check(
            f"{dep_name} keeps its own container first for kubectl logs",
            p["containers"][0]["name"] == main_c,
        )

    # 4. A rolling update deadlocks under strict anti-affinity: with one node
    #    per party the replacement pod has nowhere to go until the old one
    #    leaves, so the rollout never completes.
    print("\nscheduling:")
    for name in ("party-1", "party-2", "party-3"):
        d = deps[name]
        check(
            f"{name} uses Recreate, not RollingUpdate",
            d["spec"].get("strategy", {}).get("type") == "Recreate",
            "a surge pod cannot be placed when every node already hosts a party",
        )
        aff = (
            d["spec"]["template"]["spec"]
            .get("affinity", {})
            .get("podAntiAffinity", {})
        )
        check(
            f"{name} spreads across nodes as a hard requirement",
            bool(aff.get("requiredDuringSchedulingIgnoredDuringExecution")),
            "co-located key shares are one failure domain wearing three hats",
        )

    # The other direction: with mTLS off the plain-HTTP deployment must keep
    # working, or this whole guard just trades one broken configuration for
    # another.
    print("\nmTLS disabled (the default path must still work):")
    docs = render()
    deps = deployments(docs)
    pod = deps["party-1"]["spec"]["template"]["spec"]
    mpc = container_named(pod, "mpc-party")
    for probe in ("livenessProbe", "readinessProbe"):
        check(
            f"{probe} targets the main port when there is no mTLS",
            mpc[probe]["httpGet"]["port"] == "http",
        )
    gw = container_named(
        deps["ci-openfireblocks-api-gateway"]["spec"]["template"]["spec"],
        "api-gateway",
    )
    check(
        "the party endpoint template is plain http when mTLS is off",
        env_of(gw)["MPC_PARTY_ENDPOINT_TEMPLATE"].startswith("http://"),
    )
    check(
        "no renewal sidecar is added when certificates are not auto-issued",
        container_named(pod, "vault-pki-renew") is None,
    )

    print()
    if failures:
        print(f"{len(failures)} of {checks} invariants FAILED:")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"all {checks} chart invariants hold")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
