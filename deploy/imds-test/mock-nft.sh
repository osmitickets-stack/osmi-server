#!/bin/sh
printf '%s\n' "$*" >> /tmp/nft-actions
case "$1 $2" in
    'list table'|'delete table') exit 0 ;;
    *) exit 2 ;;
esac
