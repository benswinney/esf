#!/usr/bin/env bash
# Report each missing Cube egress host rule by name. Exit 1 when one is missing.
# The script reads state only; it changes nothing.
set -euo pipefail
CUBE_L7_MARK_HTTP=0xCE010000
CUBE_L7_MARK_HTTPS=0xCE020000
CUBE_L7_MARK_MASK=0xFFFF0000
if [[ -f /etc/cubeegress/l7-marks.conf ]]; then
  # shellcheck source=/dev/null
  . /etc/cubeegress/l7-marks.conf
fi
hex() { printf '0x%x' "$(($1))"; }
mask=$(hex "$CUBE_L7_MARK_MASK")
missing=0
report() { echo "missing: $1"; missing=1; }

marks=("$(hex "$CUBE_L7_MARK_HTTP")" "$(hex "$CUBE_L7_MARK_HTTPS")")

rules=$(ip -4 rule show)
for mark in "${marks[@]}"; do
  sel="fwmark $mark/$mask lookup 100"
  grep -qF -- "$sel" <<<"$rules" || report "ip rule $sel"
done
ip -4 route show table 100 | grep -q '^local default dev lo' ||
  report "ip route table 100 local default dev lo"
mangle=$(iptables -t mangle -S)
grep -qxF -- '-A PREROUTING -j TRANSPROXY' <<<"$mangle" ||
  report "iptables mangle PREROUTING jump to TRANSPROXY"
for mark in "${marks[@]}"; do
  grep -qE -- "^-A TRANSPROXY .*--mark $mark/$mask -j TPROXY" <<<"$mangle" ||
    report "iptables TRANSPROXY TPROXY rule for mark $mark"
done
exit "$missing"
