# Endpoint, physical and remote working policy

ISO/IEC 27001:2022 A.6.7, A.7.7–7.10, A.7.13, A.7.14, A.8.1, A.8.7, A.8.23. SOC 2 CC6.4, CC6.8.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet approved.** Review: yearly.

FORGE works remotely. Laptops are the main physical asset, so this policy is about them.

## 1. Device requirements (checked by the Sprinto agent)

| Requirement | Setting |
|---|---|
| Full-disk encryption | FileVault (macOS), BitLocker (Windows), LUKS (Linux) |
| Screen lock | Automatic after at most 5 minutes; password, biometric or security key to unlock |
| Operating system | A supported version; updates installed as in the vulnerability policy |
| Malware protection | Built-in protection on (XProtect, Defender) or an approved product |
| Firewall | On |
| Password manager | The company password manager [to complete] |
| Browser and DNS protection | Safe-browsing on; DNS filtering for malicious domains [to complete: product, e.g. a protective DNS resolver] |

Personal devices may be used for company email and documents only through the browser or an
approved app with MFA. Source code, cloud administration and signing operations are done only on
devices that meet the table above.

## 2. Working remotely

- Work on a trusted network. On public Wi-Fi, use the company VPN [to complete, if any] or a
  personal hotspot.
- Do not let others use a work device. Lock the screen when stepping away.
- Take care that screens are not overlooked when handling Confidential information, and do not
  hold calls about customer security matters in public places.

## 3. Clear desk and clear screen

No Restricted information on paper. Confidential paper is kept out of sight and shredded. Close
customer documents and the console when sharing a screen.

## 4. Storage media and recovery material

USB drives are not used for company data except encrypted media that is part of a documented
key-recovery procedure. Recovery material for signing keys is kept as `docs/deployment/KEY-RECOVERY.md`
describes, never at the same location as another party's material.

## 5. Loss or theft

Report a lost or stolen device to the incident lead at once. The device is locked and wiped
remotely where possible, and credentials used on it are rotated.

## 6. Travel

Laptops are carried, not checked in. On entering a country where devices may be inspected, carry
the minimum data needed.
