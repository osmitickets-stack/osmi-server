#!/bin/sh
printf '%s\n' "$*" >> /tmp/systemctl-actions
case "$1" in
    is-active) [ "${MOCK_DOCKER_ACTIVE:-0}" = 1 ] ;;
    *) exit 0 ;;
esac
