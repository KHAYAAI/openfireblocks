# Risk management policy

ISO/IEC 27001:2022 clauses 6.1, 8.2, 8.3. SOC 2 CC3.1–CC3.4, CC9.1.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. When risk is assessed

- A full assessment once a year.
- A focused assessment before: a new product capability that moves funds or handles personal
  data, a new Critical supplier, hosting the platform for a customer, entering a new country,
  or after a significant incident.
- The register is checked every quarter for changes in likelihood, impact or treatment status.

## 2. How

The method (scales, risk levels, acceptance criteria, treatment options) is in
`03-risk-register.md`, section 1. Every risk has an owner who is responsible for its treatment.

## 3. Fraud risk

The assessment considers fraud explicitly (SOC 2 CC3.3): insider theft of funds, collusion
between approvers, manipulation of an AI agent, false Travel Rule data, social engineering of
staff. Controls are designed so that fraud needs at least two people to collude, and leaves an
immutable record.

## 4. Decisions

- Low risks may be accepted by the ISMS owner.
- Medium risks may be accepted by the CEO with a reason.
- High and Critical risks must be treated; residual risk above Medium needs the CEO's signature
  and a review date.
- Risk decisions are recorded in the register and reported at the management review.

## 5. Customers' risk

Customers run the product in their own environments and own their risk decisions there. FORGE
supports them with the threat model, the security documentation, the access review, and
release notes that state the security effect of each change.
