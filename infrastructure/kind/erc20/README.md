# A token to drill against

`DrillToken.sol` is a minimal ERC-20 whose only purpose is to exist on the
kind cluster's development chain so the stablecoin path can be exercised
end to end: registered, verified against its own `symbol()` and
`decimals()`, transferred through a threshold signing ceremony, and read
back as a balance.

`DrillToken.bin` is its creation bytecode, compiled with

```
solc 0.8.24+commit.e11b9ed9, optimizer enabled, 200 runs
```

## Why the bytecode is committed

The drill deploys this contract on a chain that lasts for the length of a
CI run. It could compile the source each time, which would mean every run
of the drill depends on fetching a compiler — a network dependency, a
version that can drift, and a new way for a drill to go red for a reason
that has nothing to do with the platform.

Committing the artefact makes the drill deterministic. The source sits
beside it so the bytecode is auditable rather than opaque, and
`npx solc@0.8.24` over `DrillToken.sol` reproduces it.

## What it is not

Not a stablecoin, and not a model of one. Real issuers add pausing,
blocklists, upgrade proxies and — in USDT's case — a `transfer` that
returns no value at all. This contract is the smallest thing that answers
`symbol()`, `decimals()`, `balanceOf()` and `transfer()` correctly, which
is what the platform's own code paths need to be proven against.

It takes its symbol and decimals as constructor arguments precisely so the
drill can deploy the *same* contract twice with different decimals, and
show that the platform's arithmetic follows the registry rather than an
assumption. That is the failure mode worth a drill: USDC is six decimals
and DAI is eighteen, and reading one for the other is a factor of 10^12
on a number that is about to move money.
