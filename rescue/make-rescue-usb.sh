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
is_iso(){ [ -f "$1" ] && [ "$(stat -c%s "$1" 2>/dev/null || echo 0)" -gt 400000000 ] && [ "$(dd if="$1" bs=1 skip=32769 count=5 2>/dev/null)" = CD001 ]; }
usage(){
  echo "Usage: sudo $0 -d <device> [-i <systemrescue.iso>]   (ISO auto-downloads if omitted)"
  echo "Removable disks:"
  lsblk -dpno NAME,SIZE,MODEL,TRAN 2>/dev/null | grep -i usb || lsblk -dpno NAME,SIZE,MODEL
  exit 1
}
while [ $# -gt 0 ]; do case "$1" in
  -d) DEV="$2"; shift 2;; -i) ISO="$2"; shift 2;; -h|--help) usage;; *) die "unknown option $1";;
esac; done
true  # device is auto-detected below if not given
[ "$(id -u)" = 0 ] || exec sudo -- "$0" ${DEV:+-d "$DEV"} ${ISO:+-i "$ISO"}

# Tools we need; install the missing ones via the distro's package manager (we are root).
ensure_tools(){
  local miss=""
  command -v sgdisk     >/dev/null || miss="$miss gptfdisk"
  command -v mkfs.vfat  >/dev/null || miss="$miss dosfstools"
  command -v partprobe  >/dev/null || miss="$miss parted"
  [ -n "$miss" ] || return 0
  echo "Missing tools needed to build the stick:$miss"
  if   command -v pacman >/dev/null; then set -- pacman -S --needed --noconfirm $miss
  elif command -v apt-get >/dev/null; then miss=${miss/gptfdisk/gdisk}; apt-get update -qq || true; set -- apt-get install -y $miss
  elif command -v dnf    >/dev/null; then miss=${miss/gptfdisk/gdisk}; set -- dnf install -y $miss
  elif command -v zypper >/dev/null; then set -- zypper --non-interactive install $miss
  else die "please install:$miss  (couldn't find a package manager to do it automatically)"; fi
  echo "==> Installing:$miss"
  "$@" || die "couldn't install$miss automatically — install them yourself and re-run"
  for t in sgdisk mkfs.vfat partprobe; do command -v "$t" >/dev/null || die "'$t' still missing after install"; done
}
ensure_tools

if [ -z "$DEV" ]; then
  CANDS=$(lsblk -dpno NAME,RM,TRAN 2>/dev/null | awk '($2==1)||($3=="usb"){print $1}')
  N=$(printf '%s\n' "$CANDS" | grep -c . || true)
  if [ "${N:-0}" -eq 0 ]; then die "no USB stick detected — plug one in, or pass -d /dev/sdX (see: lsblk -dpo NAME,SIZE,MODEL,TRAN)"; fi
  if [ "$N" -eq 1 ]; then DEV="$CANDS"; echo "Found USB: $DEV ($(lsblk -dno SIZE,MODEL "$DEV" | tr -s ' '))";
  else
    echo "More than one removable disk is plugged in:"
    printf '%s\n' "$CANDS" | while read -r d; do echo "  $d   $(lsblk -dno SIZE,MODEL "$d" | tr -s ' ')"; done
    read -rp "Which one? (e.g. /dev/sdc): " DEV
    [ -n "$DEV" ] || die "no device chosen"
  fi
fi
[ -b "$DEV" ] || die "$DEV is not a block device. Pick a USB disk from: lsblk -dpo NAME,SIZE,MODEL,TRAN"
if [ "$(lsblk -dno TYPE "$DEV" 2>/dev/null)" = part ]; then
  die "$DEV is a partition. Use the WHOLE disk instead, e.g. ${DEV%[0-9]*} (not ${DEV}). The script wipes and repartitions the whole stick."
fi
if [ -z "$ISO" ]; then
  ISO="$SELF/systemrescue-latest.iso"
  if is_iso "$ISO"; then
    echo "Using the SystemRescue ISO already downloaded ($ISO)."
  else
    echo "==> Downloading the latest SystemRescue ISO (about 900 MB, one time)"
    URL="https://sourceforge.net/projects/systemrescuecd/files/latest/download"
    if command -v curl >/dev/null; then curl -fL --retry 3 -o "$ISO.part" "$URL"
    elif command -v wget >/dev/null; then wget -O "$ISO.part" "$URL"
    else die "need curl or wget to download the ISO (or pass one with -i)"; fi
    is_iso "$ISO.part" || { rm -f "$ISO.part"; die "that download didn't look like a bootable ISO (network/mirror hiccup). Try again, or download one yourself from https://www.system-rescue.org/Download/ and pass it with -i"; }
    mv "$ISO.part" "$ISO"
  fi
fi
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
