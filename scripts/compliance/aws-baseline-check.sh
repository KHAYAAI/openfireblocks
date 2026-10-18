#!/usr/bin/env bash
# AWS baseline check (read-only).
#
# Checks the account settings that SOC 2 CC6.1, CC6.6, CC6.7, CC7.2 and
# ISO 27001 A.5.23, A.8.2, A.8.5, A.8.9, A.8.15, A.8.16, A.8.20, A.8.24 depend
# on, as the cloud services policy (docs/compliance/isms/policies/CLOUD-SERVICES.md)
# requires: root MFA and no root keys, MFA and key age for IAM users,
# CloudTrail, GuardDuty, AWS Config, EBS default encryption, S3 public access
# and encryption, open security groups, IMDSv2, RDS encryption and exposure.
#
#   scripts/compliance/aws-baseline-check.sh [--regions "af-south-1 eu-west-1"] [--all-regions]
#                                            [--json out.json] [--no-fail]
#
# Uses the aws CLI with whatever credentials are configured (AWS_PROFILE etc.)
# and jq. It only calls Get/List/Describe APIs, plus GenerateCredentialReport
# (which creates the IAM credential report, a read-only artefact). Give it
# the AWS-managed SecurityAudit policy. Anything it cannot read is reported
# UNKNOWN, never PASS.
#
# Exit status: 1 if any check FAILs (unless --no-fail), else 0.
set -uo pipefail

REGIONS="${AWS_CHECK_REGIONS:-af-south-1 eu-west-1 us-east-1}"
ALL_REGIONS=0
JSON_OUT=""
NO_FAIL=0
KEY_MAX_AGE_DAYS="${KEY_MAX_AGE_DAYS:-90}"
while [ $# -gt 0 ]; do
  case "$1" in
    --regions) REGIONS="$2"; shift 2 ;;
    --all-regions) ALL_REGIONS=1; shift ;;
    --json) JSON_OUT="$2"; shift 2 ;;
    --no-fail) NO_FAIL=1; shift ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
command -v aws >/dev/null || { echo "the aws CLI is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

RESULTS=()
FAILS=0
record() { # status id message
  printf '%-8s %-30s %s\n' "$1" "$2" "$3"
  RESULTS+=("$(jq -cn --arg s "$1" --arg i "$2" --arg m "$3" '{status:$s,id:$i,message:$m}')")
  [ "$1" = FAIL ] && FAILS=$((FAILS + 1))
  return 0
}
# a [aws args...] -> JSON on stdout, non-zero on error (error text on stderr suppressed).
a() { aws --output json "$@" 2>/dev/null; }
now=$(date -u +%s)
age_days() { # ISO timestamp -> whole days ago (empty if not a date)
  local t; t=$(date -u -d "$1" +%s 2>/dev/null) || return 0
  echo $(( (now - t) / 86400 ))
}

ident=$(a sts get-caller-identity) || { echo "cannot call sts get-caller-identity: are credentials configured?" >&2; exit 2; }
account=$(jq -r .Account <<<"$ident")
echo "AWS baseline check: account $account as $(jq -r .Arn <<<"$ident")  ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
if [ "$ALL_REGIONS" = 1 ]; then
  REGIONS=$(a ec2 describe-regions --query 'Regions[].RegionName' | jq -r '.[]' | tr '\n' ' ')
fi
echo "Regions: $REGIONS"
echo

# ---------------------------------------------------------------- IAM (global)
if summ=$(a iam get-account-summary); then
  mfa=$(jq -r '.SummaryMap.AccountMFAEnabled' <<<"$summ")
  keys=$(jq -r '.SummaryMap.AccountAccessKeysPresent' <<<"$summ")
  [ "$mfa" = 1 ] && record PASS iam.root_mfa "Root user has MFA." || record FAIL iam.root_mfa "Root user has NO MFA."
  [ "$keys" = 0 ] && record PASS iam.root_access_keys "Root user has no access keys." || record FAIL iam.root_access_keys "Root user HAS access keys. Delete them."
else
  record UNKNOWN iam.root "Cannot read the IAM account summary."
fi

a iam generate-credential-report >/dev/null
report=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if r=$(a iam get-credential-report); then report=$(jq -r .Content <<<"$r" | base64 -d); break; fi
  sleep 2
done
if [ -z "$report" ]; then
  record UNKNOWN iam.users "Cannot read the IAM credential report."
else
  # Columns: user,arn,user_creation_time,password_enabled,password_last_used,password_last_changed,
  # password_next_rotation,mfa_active,access_key_1_active,access_key_1_last_rotated,...,access_key_2_active(14),access_key_2_last_rotated(15)
  users_json=$(tail -n +2 <<<"$report" | jq -R -s 'split("\n") | map(select(length>0) | split(",")) |
    map({user:.[0], password:.[3], mfa:.[7], k1:.[8], k1rot:.[9], k1used:.[10], k2:.[13], k2rot:.[14], k2used:.[15]})')
  people=$(jq '[.[] | select(.user != "<root_account>")] | length' <<<"$users_json")
  nomfa=$(jq -r '[.[] | select(.user != "<root_account>" and .password == "true" and .mfa != "true") | .user] | join(", ")' <<<"$users_json")
  [ -z "$nomfa" ] && record PASS iam.user_mfa "Every IAM user with a console password has MFA ($people IAM users)." \
    || record FAIL iam.user_mfa "IAM users with a console password and no MFA: $nomfa"
  old=""; unused=""
  while IFS=$'\t' read -r u active rot used; do
    [ "$active" = true ] || continue
    d=$(age_days "$rot")
    [ -n "$d" ] && [ "$d" -gt "$KEY_MAX_AGE_DAYS" ] && old="$old $u(${d}d)"
    [ "$used" = N/A ] && unused="$unused $u"
  done < <(jq -r '.[] | select(.user != "<root_account>") | [.user,.k1,.k1rot,.k1used], [.user,.k2,.k2rot,.k2used] | @tsv' <<<"$users_json")
  [ -z "$old" ] && record PASS iam.key_age "No active access key older than $KEY_MAX_AGE_DAYS days." \
    || record FAIL iam.key_age "Active access keys older than $KEY_MAX_AGE_DAYS days:$old"
  [ -z "$unused" ] || record WARN iam.key_unused "Active access keys never used:$unused (delete them)."
  keyed=$(jq '[.[] | select(.user != "<root_account>" and (.k1 == "true" or .k2 == "true"))] | length' <<<"$users_json")
  [ "$keyed" -gt 0 ] && record WARN iam.user_keys "$keyed IAM user(s) have long-lived access keys. Prefer IAM Identity Center for people and roles for machines."
fi

if pp=$(a iam get-account-password-policy); then
  len=$(jq -r '.PasswordPolicy.MinimumPasswordLength' <<<"$pp")
  [ "$len" -ge 14 ] && record PASS iam.password_policy "Password policy minimum length $len." \
    || record WARN iam.password_policy "Password policy minimum length $len (policy asks for 14)."
else
  record WARN iam.password_policy "No account password policy set (matters only if IAM users have console passwords)."
fi

# ----------------------------------------------------------- CloudTrail
if trails=$(a cloudtrail describe-trails --include-shadow-trails); then
  good=""
  while IFS=$'\t' read -r arn home validation; do
    st=$(a cloudtrail get-trail-status --name "$arn" --region "$home") || continue
    if [ "$(jq -r .IsLogging <<<"$st")" = true ]; then
      good="$arn"
      [ "$validation" = true ] && record PASS cloudtrail.validation "Log file validation on ($arn)." \
        || record FAIL cloudtrail.validation "Log file validation OFF on $arn."
      break
    fi
  done < <(jq -r '.trailList[] | select(.IsMultiRegionTrail == true) | [.TrailARN, .HomeRegion, (.LogFileValidationEnabled|tostring)] | @tsv' <<<"$trails")
  [ -n "$good" ] && record PASS cloudtrail.multi_region "A multi-region trail is logging." \
    || record FAIL cloudtrail.multi_region "No multi-region CloudTrail trail is logging."
else
  record UNKNOWN cloudtrail "Cannot read CloudTrail trails."
fi

# --------------------------------------------------------- S3 (global)
if pab=$(a s3control get-public-access-block --account-id "$account"); then
  allon=$(jq '[.PublicAccessBlockConfiguration[]] | all' <<<"$pab")
  [ "$allon" = true ] && record PASS s3.account_public_access_block "Account-level S3 Block Public Access fully on." \
    || record FAIL s3.account_public_access_block "Account-level S3 Block Public Access not fully on: $(jq -c .PublicAccessBlockConfiguration <<<"$pab")"
else
  record FAIL s3.account_public_access_block "No account-level S3 Block Public Access configuration."
fi
if buckets=$(a s3api list-buckets); then
  pub=""; noenc=""; n=0
  for b in $(jq -r '.Buckets[].Name' <<<"$buckets"); do
    n=$((n + 1))
    ps=$(a s3api get-bucket-policy-status --bucket "$b") && [ "$(jq -r .PolicyStatus.IsPublic <<<"$ps")" = true ] && pub="$pub $b"
    a s3api get-bucket-encryption --bucket "$b" >/dev/null || noenc="$noenc $b"
  done
  [ -z "$pub" ] && record PASS s3.public_buckets "No bucket policy makes a bucket public ($n buckets)." \
    || record FAIL s3.public_buckets "Public buckets:$pub"
  [ -z "$noenc" ] && record PASS s3.encryption "Every bucket has default encryption ($n buckets)." \
    || record WARN s3.encryption "No readable default encryption on:$noenc"
else
  record UNKNOWN s3.buckets "Cannot list buckets."
fi

# ------------------------------------------------------------ per region
for r in $REGIONS; do
  # EBS encryption by default
  if e=$(a ec2 get-ebs-encryption-by-default --region "$r"); then
    [ "$(jq -r .EbsEncryptionByDefault <<<"$e")" = true ] && record PASS "ebs.default_encryption[$r]" "EBS encryption by default on." \
      || record FAIL "ebs.default_encryption[$r]" "EBS encryption by default OFF."
  else
    record UNKNOWN "ebs.default_encryption[$r]" "Cannot read (region not enabled?)."; continue
  fi

  # GuardDuty
  if d=$(a guardduty list-detectors --region "$r"); then
    det=$(jq -r '.DetectorIds[0] // empty' <<<"$d")
    if [ -z "$det" ]; then record FAIL "guardduty[$r]" "GuardDuty not enabled."
    else
      st=$(a guardduty get-detector --detector-id "$det" --region "$r" | jq -r '.Status // "UNKNOWN"')
      [ "$st" = ENABLED ] && record PASS "guardduty[$r]" "GuardDuty enabled." || record FAIL "guardduty[$r]" "GuardDuty detector status $st."
    fi
  else
    record UNKNOWN "guardduty[$r]" "Cannot read GuardDuty."
  fi

  # AWS Config
  if c=$(a configservice describe-configuration-recorder-status --region "$r"); then
    rec=$(jq '[.ConfigurationRecordersStatus[] | select(.recording == true)] | length' <<<"$c")
    [ "$rec" -gt 0 ] && record PASS "config[$r]" "AWS Config recording." || record WARN "config[$r]" "AWS Config not recording."
  else
    record UNKNOWN "config[$r]" "Cannot read AWS Config."
  fi

  # Security groups open to the internet
  if sgs=$(a ec2 describe-security-groups --region "$r"); then
    bad=$(jq -r '
      .SecurityGroups[] as $g | $g.IpPermissions[] as $p
      | select(([$p.IpRanges[]?.CidrIp] + [$p.Ipv6Ranges[]?.CidrIpv6]) | any(. == "0.0.0.0/0" or . == "::/0"))
      | ($p.FromPort // 0) as $f | ($p.ToPort // 65535) as $t
      | select($p.IpProtocol == "-1" or ($f <= 22 and $t >= 22) or ($f <= 3389 and $t >= 3389)
               or ($f <= 5432 and $t >= 5432) or ($f <= 6379 and $t >= 6379) or ($f <= 8200 and $t >= 8200))
      | "\($g.GroupId)(\($g.GroupName)):\(if $p.IpProtocol == "-1" then "all" else "\($f)-\($t)" end)"' <<<"$sgs" | sort -u | tr '\n' ' ')
    [ -z "$bad" ] && record PASS "sg.open_admin_ports[$r]" "No security group opens SSH, RDP, Postgres, Redis, Vault or all ports to the internet." \
      || record FAIL "sg.open_admin_ports[$r]" "Open to 0.0.0.0/0 or ::/0: $bad"
  else
    record UNKNOWN "sg[$r]" "Cannot read security groups."
  fi

  # IMDSv2 on running instances
  if inst=$(a ec2 describe-instances --region "$r" --filters Name=instance-state-name,Values=running,stopped); then
    v1=$(jq -r '[.Reservations[].Instances[] | select(.MetadataOptions.HttpTokens != "required") | .InstanceId] | join(" ")' <<<"$inst")
    n=$(jq '[.Reservations[].Instances[]] | length' <<<"$inst")
    [ -z "$v1" ] && record PASS "ec2.imdsv2[$r]" "All $n instances require IMDSv2." || record FAIL "ec2.imdsv2[$r]" "IMDSv1 allowed on: $v1"
  fi

  # RDS
  if dbs=$(a rds describe-db-instances --region "$r"); then
    n=$(jq '.DBInstances | length' <<<"$dbs")
    if [ "$n" -gt 0 ]; then
      ue=$(jq -r '[.DBInstances[] | select(.StorageEncrypted != true) | .DBInstanceIdentifier] | join(" ")' <<<"$dbs")
      pa=$(jq -r '[.DBInstances[] | select(.PubliclyAccessible == true) | .DBInstanceIdentifier] | join(" ")' <<<"$dbs")
      [ -z "$ue" ] && record PASS "rds.encryption[$r]" "All $n RDS instances encrypted." || record FAIL "rds.encryption[$r]" "Unencrypted RDS: $ue"
      [ -z "$pa" ] && record PASS "rds.public[$r]" "No RDS instance is publicly accessible." || record FAIL "rds.public[$r]" "Publicly accessible RDS: $pa"
    fi
  fi
done

echo
pass=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="PASS")]|length')
warn=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="WARN")]|length')
unk=$(printf '%s\n' "${RESULTS[@]}" | jq -s '[.[]|select(.status=="UNKNOWN")]|length')
echo "Summary: $pass PASS, $FAILS FAIL, $warn WARN, $unk UNKNOWN"

if [ -n "$JSON_OUT" ]; then
  printf '%s\n' "${RESULTS[@]}" | jq -s --arg acct "$account" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg regions "$REGIONS" \
    '{check:"aws-baseline", account:$acct, regions:($regions|split(" ")|map(select(length>0))), at:$at, results:.}' > "$JSON_OUT"
  echo "Wrote $JSON_OUT"
fi
[ "$FAILS" -gt 0 ] && [ "$NO_FAIL" -eq 0 ] && exit 1
exit 0
