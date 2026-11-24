#!/bin/sh
case "$*" in
    *latest/api/token*)
        [ "${MOCK_TOKEN_FAIL:-0}" = 0 ] || exit 7
        printf mock-token
        ;;
    *iam/security-credentials/*)
        printf '%s' "${MOCK_ROLE_STATUS:-404}"
        ;;
    *) exit 2 ;;
esac
