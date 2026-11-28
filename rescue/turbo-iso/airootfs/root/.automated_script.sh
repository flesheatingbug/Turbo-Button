#!/usr/bin/env bash
# archiso runs this on login; only take over the main console.
script_cmdline(){ local param; for param in $(</proc/cmdline); do case "$param" in script=*) echo "${param#script=}"; return 0;; esac; done; }
if [[ $(tty) == "/dev/tty1" ]]; then
  exec /usr/local/bin/turbo-start
fi
