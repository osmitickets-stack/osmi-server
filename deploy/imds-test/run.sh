#!/bin/sh
set -eu

policy=/opt/osmi/deploy/imds-email-worker.nft
checker=/opt/osmi/deploy/ensure-imds-email-guard.sh
guard_unit=/etc/systemd/system/osmi-email-imds-guard.service
docker_dropin=/etc/systemd/system/docker.service.d/90-osmi-email-imds-guard.conf

prepare() {
    rm -f "$policy" "$checker" "$guard_unit" "$docker_dropin"
    cp /fixtures/imds-email-worker.nft "$policy"
    cp /fixtures/ensure-imds-email-guard.sh "$checker"
    cp /fixtures/osmi-email-imds-guard.service "$guard_unit"
    cp /fixtures/docker-imds-guard.conf "$docker_dropin"
    : > /tmp/systemctl-actions
    : > /tmp/nft-actions
}

assert_removed() {
    [ ! -e "$policy" ] && [ ! -e "$checker" ] \
        && [ ! -e "$guard_unit" ] && [ ! -e "$docker_dropin" ]
}

prepare
MOCK_ROLE_STATUS=404 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output
grep -Fq IMDS_GUARD_ROLLBACK_OK /tmp/rollback-output
assert_removed
grep -Fq 'start docker.service' /tmp/systemctl-actions
grep -Fq 'delete table inet osmi_email_imds' /tmp/nft-actions

prepare
MOCK_ROLE_STATUS=404 MOCK_DOCKER_ACTIVE=1 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output
grep -Fq IMDS_GUARD_ROLLBACK_OK /tmp/rollback-output
assert_removed
if grep -Fq 'start docker.service' /tmp/systemctl-actions; then
    echo HEALTHY_DOCKER_MUST_NOT_RESTART
    exit 1
fi

prepare
if MOCK_ROLE_STATUS=200 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output; then
    echo ROLE_PRESENT_MUST_FAIL
    exit 1
fi
[ -e "$docker_dropin" ] && [ -e "$policy" ]
[ ! -s /tmp/nft-actions ]

prepare
printf 'altered\n' >> "$policy"
if MOCK_ROLE_STATUS=404 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output; then
    echo ALTERED_FILE_MUST_FAIL
    exit 1
fi
grep -Fq ROLLBACK_FILE_MISMATCH_GUARD_RETAINED /tmp/rollback-output
[ -e "$docker_dropin" ] && [ -e "$policy" ]
[ ! -s /tmp/nft-actions ]

prepare
if MOCK_TOKEN_FAIL=1 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output 2>/dev/null; then
    echo UNKNOWN_IAM_MUST_FAIL
    exit 1
fi
[ -e "$docker_dropin" ] && [ -e "$policy" ]
[ ! -s /tmp/nft-actions ]

prepare
rm "$checker"
MOCK_ROLE_STATUS=404 /bin/sh /tests/rollback-imds-email-guard.sh > /tmp/rollback-output
grep -Fq IMDS_GUARD_ROLLBACK_OK /tmp/rollback-output
assert_removed

echo IMDS_ROLLBACK_ISOLATED_PASS
