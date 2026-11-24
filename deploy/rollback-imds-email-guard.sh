#!/bin/sh
set -eu

policy=/opt/osmi/deploy/imds-email-worker.nft
checker=/opt/osmi/deploy/ensure-imds-email-guard.sh
guard_unit=/etc/systemd/system/osmi-email-imds-guard.service
docker_dropin=/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf

# Never expose instance credentials if IAM appeared during the rollout.
token="$(curl --noproxy '*' -fsS --max-time 3 -X PUT \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' \
    'http://169.254.169.254/latest/api/token')" || {
    echo IMDS_STATUS_UNKNOWN_GUARD_RETAINED
    exit 1
}
status="$(curl --noproxy '*' -sS --output /dev/null --write-out '%{http_code}' \
    --max-time 3 -H "X-aws-ec2-metadata-token: $token" \
    'http://169.254.169.254/latest/meta-data/iam/security-credentials/')" || {
    echo IMDS_STATUS_UNKNOWN_GUARD_RETAINED
    exit 1
}
unset token
if [ "$status" != 404 ]; then
    echo IAM_STATUS_CHANGED_GUARD_RETAINED
    exit 1
fi

# Only remove the exact files staged by this release. Missing files are fine
# because the timer can fire during a partial installation.
check_if_present() {
    path=$1
    expected=$2
    if [ -e "$path" ] || [ -L "$path" ]; then
        [ -f "$path" ] && [ ! -L "$path" ] || return 1
        actual="$(sha256sum "$path" | cut -d' ' -f1)"
        [ "$actual" = "$expected" ] || return 1
    fi
}

check_if_present "$policy" ad2eba080089c6d9bb5e9611de03e317b0344a9ee50efb563459a28d1c69025d || {
    echo ROLLBACK_FILE_MISMATCH_GUARD_RETAINED; exit 1;
}
check_if_present "$checker" a05d06d0f292cdb262335215cb659fcd8dbd8879f909cc65d1e7e0b543157d13 || {
    echo ROLLBACK_FILE_MISMATCH_GUARD_RETAINED; exit 1;
}
check_if_present "$guard_unit" 63740459f4951217582cdfbb8593d6bfc96c2e528b665e0f3d9c725109efeb8b || {
    echo ROLLBACK_FILE_MISMATCH_GUARD_RETAINED; exit 1;
}
check_if_present "$docker_dropin" 005c70ff331b0ff22f42dfe20008603fa6092cb5023c2735fe0368d0032d231d || {
    echo ROLLBACK_FILE_MISMATCH_GUARD_RETAINED; exit 1;
}

rm -f -- "$docker_dropin"
systemctl daemon-reload
systemctl stop osmi-email-imds-guard.service || true
if /usr/sbin/nft list table inet osmi_email_imds >/dev/null 2>&1; then
    /usr/sbin/nft delete table inet osmi_email_imds
fi
rm -f -- "$guard_unit" "$checker" "$policy"
systemctl daemon-reload

if ! systemctl is-active --quiet docker.service; then
    systemctl reset-failed docker.service || true
    systemctl start docker.service
fi
echo IMDS_GUARD_ROLLBACK_OK
