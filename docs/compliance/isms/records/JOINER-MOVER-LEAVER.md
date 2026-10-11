# Joiners, movers and leavers (template)

ISO/IEC 27001:2022 A.5.11, A.5.16, A.5.18, A.6.1, A.6.2, A.6.5. SOC 2 CC1.4, CC6.2, CC6.3.

## Joiner

```
Name:                     Role:                   Start date:           Manager:
[ ] Screening completed (date, provider; result kept per retention policy)   — before access to production/signing/customer data
[ ] Contract / agreement with confidentiality and security clauses signed
[ ] Policies acknowledged (acceptable use, code of conduct, information security policy)
[ ] Laptop issued or approved; Sprinto agent installed and compliant
[ ] Accounts created (list each with role): Google __ GitHub __ AWS __ Sprinto __ other __
[ ] MFA confirmed on each account
[ ] Security awareness training completed (within 30 days)       Date:
Approved by:                            Date:
```

## Mover

```
Name:              Old role:              New role:               Date:
[ ] Access for the old role removed:
[ ] Access for the new role granted and approved by:
[ ] Signing / approval roles in staging updated
```

## Leaver

```
Name:              Last day:               Reason (optional):
On or before the last day (target: within 1 working day, objective O-3):
[ ] Google Workspace suspended; mail forwarded or delegated if needed
[ ] GitHub removed from org; personal access tokens and SSH/deploy keys revoked
[ ] AWS access removed; any access keys deleted
[ ] Sprinto, Stripe, WorkOS and other SaaS removed
[ ] Removed from every staging product organisation (People), and any agents they created reviewed
[ ] Shared secrets they knew rotated (list):
[ ] Signing-party or release-signing roles reassigned
[ ] Laptop and other assets returned or wiped (asset inventory updated)
[ ] Reminded in writing of continuing confidentiality
Completed by:                         Date and time:
```
