#!/usr/bin/env bash
# Build a tiny bootable "Turbo Button Rescue" USB that boots STRAIGHT into Turbo Button — no full
# rescue OS. Bakes the agent and your relay settings into the image, builds it with archiso, and
# (unless --iso-only) writes it to a USB stick. Run on an Arch/CachyOS box:
#
#     sudo ./build-rescue-iso.sh            # build + write to the detected USB
#     sudo ./build-rescue-iso.sh --iso-only # just build the .iso
#
# Put your relay 'turbobutton.conf' next to this script (the relay's maker download includes it)
# so the booted stick auto-connects to your relay. The agent downloads automatically if missing.
set -euo pipefail
SELF="$(cd "$(dirname "$0")" && pwd)"
PROFILE="$SELF/turbo-iso"
DEV=""; CONF="$SELF/turbobutton.conf"; ISO_ONLY=0
die(){ echo "Error: $*" >&2; exit 1; }
while [ $# -gt 0 ]; do case "$1" in
  -d) DEV="$2"; shift 2;; -c) CONF="$2"; shift 2;; --iso-only) ISO_ONLY=1; shift;;
  -h|--help) echo "Usage: sudo $0 [-d /dev/sdX] [-c turbobutton.conf] [--iso-only]"; exit 0;;
  *) die "unknown option $1";; esac; done
[ "$(id -u)" = 0 ] || exec sudo -- "$0" ${DEV:+-d "$DEV"} ${CONF:+-c "$CONF"} $([ "$ISO_ONLY" = 1 ] && echo --iso-only)
[ -d "$PROFILE" ] || die "can't find the 'turbo-iso' profile next to this script"
command -v mkarchiso >/dev/null || { echo "==> Installing archiso"; pacman -S --needed --noconfirm archiso || die "couldn't install archiso — install it and retry"; }

# Agent (download if not supplied next to the script)
AGENT="$SELF/turbobutton"
if [ ! -f "$AGENT" ]; then
  echo "==> Downloading the Turbo Button agent"
  URL="https://github.com/flesheatingbug/Turbo-Button/releases/download/latest/TurboButton-linux-x86_64"
  if command -v curl >/dev/null; then curl -fL "$URL" -o "$AGENT"; else wget -O "$AGENT" "$URL"; fi || die "couldn't download the agent; put 'turbobutton' next to this script"
fi
install -Dm755 "$AGENT" "$PROFILE/airootfs/root/turbobutton"
if [ -f "$CONF" ]; then install -Dm600 "$CONF" "$PROFILE/airootfs/root/turbobutton.conf"; echo "==> Baking in relay settings from $CONF";
else echo "!! No turbobutton.conf found — the stick will work on the same Wi-Fi but won't auto-connect to your relay."; fi

cleanup(){ rm -f "$PROFILE/airootfs/root/turbobutton" "$PROFILE/airootfs/root/turbobutton.conf"; }
trap cleanup EXIT

mkdir -p "$PROFILE/airootfs/etc/systemd/system/multi-user.target.wants"
ln -sf /usr/lib/systemd/system/NetworkManager.service "$PROFILE/airootfs/etc/systemd/system/multi-user.target.wants/NetworkManager.service"
OUT="$SELF/out"; WORK="$(mktemp -d)"; mkdir -p "$OUT"
echo "==> Building the ISO (first run downloads ~500 MB of packages; takes several minutes)"
mkarchiso -v -w "$WORK" -o "$OUT" "$PROFILE"
rm -rf "$WORK"
ISO="$(ls -t "$OUT"/*.iso 2>/dev/null | head -1)"
[ -f "$ISO" ] || die "build finished but no ISO was produced (see the log above)"
echo "==> Built: $ISO  ($(du -h "$ISO" | cut -f1))"
cleanup; trap - EXIT

[ "$ISO_ONLY" = 1 ] && { echo "Done. Write it with: sudo dd if=$ISO of=/dev/sdX bs=4M status=progress conv=fsync"; exit 0; }

# ---- write to USB (our ISO is isohybrid: a plain dd boots BIOS + UEFI) ----
if [ -z "$DEV" ]; then
  CANDS=$(lsblk -dpno NAME,RM,TRAN 2>/dev/null | awk '($2==1)||($3=="usb"){print $1}')
  N=$(printf '%s\n' "$CANDS" | grep -c . || true)
  [ "${N:-0}" -eq 0 ] && { echo "No USB detected. Write it yourself: sudo dd if=$ISO of=/dev/sdX bs=4M status=progress conv=fsync"; exit 0; }
  if [ "$N" -eq 1 ]; then DEV="$CANDS"; echo "Found USB: $DEV ($(lsblk -dno SIZE,MODEL "$DEV" | tr -s ' '))";
  else echo "Removable disks:"; printf '%s\n' "$CANDS" | while read -r d; do echo "  $d  $(lsblk -dno SIZE,MODEL "$d" | tr -s ' ')"; done; read -rp "Which one? " DEV; fi
fi
[ -b "$DEV" ] || die "$DEV is not a block device"
[ "$(lsblk -dno TYPE "$DEV" 2>/dev/null)" = disk ] || die "$DEV must be the whole disk (e.g. /dev/sdc), not a partition"
echo "About to ERASE $DEV ($(lsblk -dno SIZE,MODEL "$DEV" | tr -s ' '))."
read -rp "Type ERASE to write the rescue stick: " ok; [ "$ok" = ERASE ] || die "cancelled"
for p in $(lsblk -nro NAME "$DEV" | tail -n +2); do umount "/dev/$p" 2>/dev/null || true; done
echo "==> Writing $ISO to $DEV"
dd if="$ISO" of="$DEV" bs=4M status=progress conv=fsync
sync
echo
echo "Done. Boot the dead PC from $DEV (BIOS or UEFI) — it comes straight up in Turbo Button."
