#!/usr/bin/env bash
# Make a standalone Turbo Button rescue USB from a SystemRescue ISO (boots UEFI + legacy BIOS).
# Writes the ISO to the stick, then adds a second FAT32 partition holding Turbo Button so it runs
# automatically at boot. WIPES the target device.
#
#   sudo ./make-rescue-usb.sh -d /dev/sdX -i /path/to/systemrescue-x.y.z-amd64.iso
#
# Get the ISO from https://www.system-rescue.org . Turbo Button (the 'turbobutton' binary,
# 'turbobutton.conf' and 'turbo-rescue.sh') must sit in this script's folder (the relay's
# "Rescue USB maker" download gives you all of them together).
set -euo pipefail
DEV=""; ISO=""
SELF="$(cd "$(dirname "$0")" && pwd)"
die(){ echo "Error: $*" >&2; exit 1; }
usage(){
  echo "Usage: sudo $0 -d <device> -i <systemrescue.iso>"
  echo "Removable disks:"
  lsblk -dpno NAME,SIZE,MODEL,TRAN 2>/dev/null | grep -i usb || lsblk -dpno NAME,SIZE,MODEL
  exit 1
}
while [ $# -gt 0 ]; do case "$1" in
  -d) DEV="$2"; shift 2;; -i) ISO="$2"; shift 2;; -h|--help) usage;; *) die "unknown option $1";;
esac; done
[ -n "$DEV" ] && [ -n "$ISO" ] || usage
[ "$(id -u)" = 0 ] || exec sudo -- "$0" -d "$DEV" -i "$ISO"
[ -b "$DEV" ] || die "$DEV is not a block device"
[ -f "$ISO" ] || die "ISO not found: $ISO"
[ -f "$SELF/turbo-rescue.sh" ] || die "missing turbo-rescue.sh next to this script"
REL="https://github.com/flesheatingbug/Turbo-Button/releases/download/latest"
if [ ! -f "$SELF/turbobutton" ]; then
  echo "==> Downloading the Linux agent from GitHub"
  if command -v curl >/dev/null; then curl -fL "$REL/TurboButton-linux-x86_64" -o "$SELF/turbobutton"
  else wget -O "$SELF/turbobutton" "$REL/TurboButton-linux-x86_64"; fi
  want=$( (command -v curl >/dev/null && curl -fsL "$REL/SHA256SUMS" || wget -qO- "$REL/SHA256SUMS") | awk "/TurboButton-linux-x86_64\$/{print \$1}")
  got=$(sha256sum "$SELF/turbobutton" | awk "{print \$1}")
  [ -n "$want" ] && [ "$want" != "$got" ] && die "downloaded agent checksum mismatch (want $want got $got)"
  chmod +x "$SELF/turbobutton"
fi
for t in dd sgdisk mkfs.vfat partprobe lsblk; do command -v "$t" >/dev/null || die "need '$t' (install gptfdisk/dosfstools)"; done

# Safety: refuse the disk that holds the running system.
ROOTSRC="$(findmnt -no SOURCE / || true)"
case "$ROOTSRC" in "$DEV"*) die "$DEV looks like your system disk — refusing";; esac
SIZE=$(lsblk -dnbo SIZE "$DEV"); HUMAN=$(lsblk -dno SIZE "$DEV")
[ "$SIZE" -gt 128000000000 ] && die "$DEV is ${HUMAN} — that's large for a USB stick; refusing in case it's a hard drive. Edit the script if you're sure."
echo "About to ERASE $DEV ($HUMAN, $(lsblk -dno MODEL "$DEV"))."
read -rp "Type ERASE to continue: " ok; [ "$ok" = ERASE ] || die "cancelled"

echo "==> Unmounting any partitions on $DEV"
for p in $(lsblk -nro NAME "$DEV" | tail -n +2); do umount "/dev/$p" 2>/dev/null || true; done
echo "==> Writing SystemRescue to $DEV (this takes a few minutes)"
dd if="$ISO" of="$DEV" bs=4M status=progress conv=fsync
sync; partprobe "$DEV" 2>/dev/null || true; sleep 2

echo "==> Adding a Turbo Button data partition in the free space"
sgdisk -e "$DEV" >/dev/null 2>&1 || true            # move backup GPT header to the end
sgdisk -n 0:0:0 -t 0:0700 -c 0:TURBO "$DEV" >/dev/null || die "couldn't create the data partition (the stick may be too small — use a bigger one)"
sync; partprobe "$DEV" 2>/dev/null || true; sleep 2
# Highest-numbered partition is the one we just made.
LAST=$(lsblk -nro NAME "$DEV" | tail -1)
PART="/dev/$LAST"
[ -b "$PART" ] || die "new partition $PART didn't appear; re-plug the stick and run just the format+copy steps"
echo "==> Formatting $PART as FAT32 (TURBO)"
mkfs.vfat -n TURBO "$PART" >/dev/null

MNT=$(mktemp -d)
mount "$PART" "$MNT"
echo "==> Copying Turbo Button onto the stick"
mkdir -p "$MNT/turbo-rescue"
cp "$SELF/turbobutton" "$MNT/turbo-rescue/"
[ -f "$SELF/turbobutton.conf" ] && cp "$SELF/turbobutton.conf" "$MNT/turbo-rescue/"
cp "$SELF/turbo-rescue.sh" "$MNT/turbo-rescue/"
chmod +x "$MNT/turbo-rescue/turbobutton" "$MNT/turbo-rescue/turbo-rescue.sh"
# SystemRescue runs an 'autorun' script found on any attached filesystem when booted with the
# 'autoruns=1' option (it's offered in the boot menu). This one launches Turbo Button.
cat > "$MNT/autorun" <<'AR'
#!/bin/sh
MNT=$(dirname "$0")
exec sh "$MNT/turbo-rescue/turbo-rescue.sh"
AR
chmod +x "$MNT/autorun"
sync; umount "$MNT"; rmdir "$MNT"
echo
echo "Done. Boot the dead PC from $DEV (UEFI or BIOS), connect WiFi, and run Turbo Button:"
echo "  - easiest: at the SystemRescue boot menu pick the 'autoruns' entry, or"
echo "  - mount the TURBO partition and run turbo-rescue/turbo-rescue.sh"
