# Access review: company systems (template)

ISO/IEC 27001:2022 A.5.15, A.5.18, A.8.2. SOC 2 CC6.2, CC6.3. Quarterly.

Organisations in the product are reviewed in the console (Controls > Access review). The console
stores who signed, how they proved presence (one-time code or SSO), the snapshot and its SHA-256,
and the findings, and the record cannot be changed afterwards. This template covers the company's
own systems, which the product cannot see.

```
Review period:                 Quarter: Q_ 20__
Reviewer:                      (not the only administrator of the systems reviewed, where possible)
Date:

For each system: export the member list (Sprinto does this once connected), then answer per person.

System: Google Workspace | GitHub org | AWS (IAM Identity Center / IAM) | Sprinto | Stripe | WorkOS | other: ____

| Person / account | Role / permissions | Still needed? (Y/N) | MFA on? | Privileged? | Last used | Action (none / reduce / remove) | Done (date) |
|---|---|---|---|---|---|---|---|

Checks
[ ] No accounts of people who have left (compare with the leavers list)
[ ] No shared or generic accounts without a named owner
[ ] Privileged roles (GitHub owner, Workspace super admin, AWS admin) limited to the people named in the access control policy
[ ] AWS: root has MFA and no access keys; no IAM user access keys older than 90 days (aws-baseline-check.sh output attached)
[ ] GitHub: outside collaborators justified; deploy keys and tokens reviewed (github-controls-check.sh output attached)
[ ] Service accounts / API keys: each has an owner and is still used
[ ] Staging product organisations reviewed in the console this quarter (link the review IDs)

Outcome:  no changes needed | changes made | changes required (open a corrective action)
Notes:
Reviewer signature:                     Approved by (CEO or ISMS owner):
```
