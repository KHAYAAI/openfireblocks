# Licensing

What the licence permits, why it was chosen, and what the dependency tree
obliges you to.

**None of this is legal advice.** It is an engineer's reading of the
licence texts, written so that whoever briefs counsel can do so quickly.
Have a lawyer confirm it before you sign anything.

## The choice

OpenFireblocks is under the **Elastic License 2.0** — source-available,
not open source.

The business it has to support is specific: sell licences now to banks and
fintechs who run the software themselves, and keep the option of running a
hosted service yourself later. ELv2 is the licence that fits both halves
without conflict.

| What | Under ELv2 |
|---|---|
| A bank self-hosts it in their own data centre | **Permitted** |
| A bank runs it in their own AWS/Azure account for their own business | **Permitted** |
| A bank modifies it for their own use | **Permitted** |
| A competitor offers it to *their* customers as hosted custody | **Prohibited** |
| You offer it as hosted custody | **Permitted** — you are the licensor |

The second row is the one that matters commercially. "Cloud hosting" is
two different things: a customer running it in their own cloud is using
the software, and is fine. Someone running it *for other people* is
providing a hosted service, and is not. The line falls exactly where a
software vendor would want it.

### Why not the alternatives

**BUSL-1.1** was the obvious candidate and it is the wrong shape here. It
requires a Change Date after which the software becomes open source —
typically four years. That is fine for a company whose moat is its hosted
service today, and actively harmful for one whose plan is to *start* a
hosted service later: the conversion would hand competitors the codebase
at roughly the moment you began competing with them.

**Apache-2.0**, which this repository previously claimed in two
`package.json` files, grants everyone including competitors an
irrevocable free licence. That is incompatible with selling licences, and
it is irrevocable once published.

**AGPL-3.0 with a commercial dual licence** is the other standard way to
sell self-hosted software, and it fails on the buyer. Banks and large
fintechs frequently have blanket procurement bans on AGPL. A licence that
gets you rejected before the technical evaluation is not a licence.

**Proprietary, all rights reserved** would work, but gives up something
useful: ELv2 lets a prospect read the source before buying. For a custody
platform whose entire claim is that the private key never exists, being
able to say "read the code and check" is a sales asset, not a risk.

### The licence key clause

ELv2 prohibits circumventing licence key functionality. There is no
licence key in this software today, so the clause is currently inert. It
is worth knowing it is there: if you later add licence enforcement, the
legal backing already exists.

## Dependencies

**There is no copyleft dependency.** Everything is permissive -- MIT, ISC,
BSD, Apache-2.0, one MPL-2.0 -- and the 284-package JavaScript tree
contains no GPL or AGPL at all.

`github.com/ethereum/go-ethereum` was the exception, LGPL-3.0 and imported
by nine files. It has been removed from every module, and CI fails any
module that brings it back.

That was worth doing even though it was not, strictly, blocking. The LGPL
permits selling proprietary software that links against it; what it
requires is that whoever receives a distributed binary can relink against
their own build of the library, and Go links statically. Selling source
licences for self-hosting discharged that. Shipping images without source
would not have -- and that is precisely what a customer asks for as the
customer count grows, at which point it becomes a question in procurement
rather than a decision made calmly in advance.

See [engineering/GO-ETHEREUM-REMOVAL.md](engineering/GO-ETHEREUM-REMOVAL.md)
for what replaced it and how the replacement is proven.

## Third-party inventory

[NOTICE](NOTICE). It was compiled by reading `go.mod` files and the installed
dependency tree — a starting point for a licence review, not the product
of one. A buyer's diligence will run a scanner against this; better that
it agrees with a file you wrote than surprises you.

## Before you ship anything

1. **Replace `<LEGAL ENTITY NAME>` in NOTICE.** ELv2 defines the licensor
   as the entity offering the terms. Unnamed, there is no identifiable
   party granting the licence.
2. Have counsel review LICENSE, NOTICE and this document together.
3. Decide whether the first customers get source or binaries. Either is
   now clean; source is still the better fit for a self-hosted product.
4. Put the commercial terms — support, warranty, liability cap, escrow —
   in a signed agreement. ELv2 governs the copyright grant and says
   nothing about any of those, and a bank will want all four.
