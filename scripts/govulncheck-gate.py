#!/usr/bin/env python3
"""Runs govulncheck over every Go module and fails on any vulnerability this code
actually calls, unless it is a recorded, reasoned and unexpired acceptance.

    python3 scripts/govulncheck-gate.py            all modules
    python3 scripts/govulncheck-gate.py services/mpc-signer

govulncheck has no ignore list, and a gate that is turned off the first time it is
inconvenient protects nothing. The alternative to disabling it is this: an advisory that
cannot be fixed yet is written down in docs/security/accepted-vulnerabilities.json with the
reason, who accepted it and an expiry date. The gate then:

  * fails on any called vulnerability that is not listed;
  * fails on a listed one whose acceptance has expired (so it must be re-decided);
  * warns when a listed advisory is no longer found (so the entry can be deleted).

"Called" means govulncheck found a path from this module's code to the vulnerable symbol
(a finding with a function in its trace). Advisories in code that is merely imported or
required, and not called, are reported as a count and do not fail the gate.
"""
import datetime, json, os, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ACCEPT = os.path.join(ROOT, "docs", "security", "accepted-vulnerabilities.json")
BIN = os.environ.get("GOVULNCHECK") or os.path.join(subprocess.run(["go", "env", "GOPATH"], capture_output=True, text=True).stdout.strip(), "bin", "govulncheck")


def modules(args):
    if args:
        return args
    out = []
    for base in ("services", "sdks"):
        d = os.path.join(ROOT, base)
        for name in sorted(os.listdir(d)):
            if os.path.exists(os.path.join(d, name, "go.mod")):
                out.append(os.path.join(base, name))
    return out


def called(module):
    """OSV ids this module calls, with an example call site and the vulnerable module."""
    r = subprocess.run([BIN, "-format", "json", "./..."], cwd=os.path.join(ROOT, module), capture_output=True, text=True)
    if r.returncode not in (0, 3) and not r.stdout:
        raise SystemExit(f"govulncheck failed in {module}: {r.stderr[:500]}")
    dec = json.JSONDecoder(); s = r.stdout.lstrip(); found = {}; n_not_called = set()
    while s:
        obj, i = dec.raw_decode(s); s = s[i:].lstrip()
        f = obj.get("finding")
        if not f:
            continue
        trace = f.get("trace") or []
        if any("function" in fr for fr in trace):
            fr = trace[0]
            found.setdefault(f["osv"], {"module": fr.get("module", "?"), "fixed": f.get("fixed_version", ""), "site": next((f'{x.get("position",{}).get("filename","?")}:{x.get("position",{}).get("line","?")}' for x in trace if x.get("position")), "")})
        else:
            n_not_called.add(f["osv"])
    return found, n_not_called


def main():
    accepted = {}
    if os.path.exists(ACCEPT):
        for e in json.load(open(ACCEPT))["accepted"]:
            accepted[(e["id"], e["module_dir"])] = e
    today = datetime.date.today()
    failures, seen = [], set()
    for m in modules(sys.argv[1:]):
        found, not_called = called(m)
        print(f"--- {m}: {len(found)} called, {len(not_called)} present but not called")
        for osv, info in sorted(found.items()):
            key = (osv, m); seen.add(key)
            e = accepted.get(key)
            if not e:
                failures.append(f"{m}: {osv} in {info['module']} (fixed in {info['fixed'] or 'n/a'}) is called at {info['site']} and is not accepted")
            elif datetime.date.fromisoformat(e["expires"]) < today:
                failures.append(f"{m}: the acceptance of {osv} expired on {e['expires']}; fix it or re-decide ({e['reason'][:80]}...)")
            else:
                print(f"    accepted until {e['expires']}: {osv} ({info['module']}) -- {e['reason'][:90]}")
    for key, e in accepted.items():
        if key not in seen and (len(sys.argv) == 1):
            print(f"warning: {key[0]} is accepted for {key[1]} but was not found; delete the entry if it is fixed")
    if failures:
        print("\nFAILED:")
        for f in failures:
            print("  " + f)
        sys.exit(1)
    print("\ngovulncheck gate: no unaccepted called vulnerabilities")


main()
