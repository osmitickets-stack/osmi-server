#!/bin/sh
set -eu

nft=/usr/sbin/nft
policy=/opt/osmi/deploy/imds-email-worker.nft

case "${1:-}" in
    load)
        if ! "$nft" list table inet osmi_email_imds >/dev/null 2>&1; then
            "$nft" -c -f "$policy"
            "$nft" -f "$policy"
        fi
        ;;
    check) ;;
    *) exit 2 ;;
esac

rules="$("$nft" -y list chain inet osmi_email_imds deny_other_containers)"
printf '%s\n' "$rules" | grep -Fq 'type filter hook forward priority -10; policy accept;'
printf '%s\n' "$rules" | grep -Fq 'ip daddr 169.254.169.254 iifname != "br-osmi-email" drop'
printf '%s\n' "$rules" | grep -Fq 'ip6 daddr fd00:ec2::254 iifname != "br-osmi-email" drop'
