#!/bin/sh
# Creates a stable signing key for the APK (run once, keep the .jks and password safe).
# Then add two GitHub repository secrets:
#   TURBO_KEYSTORE_B64  = output of: base64 -w0 turbobutton.jks
#   TURBO_KEYSTORE_PASS = the password you chose
set -e
printf "Choose a keystore password (6+ chars): "; stty -echo; read PASS; stty echo; echo
keytool -genkeypair -keystore turbobutton.jks -alias turbobutton -keyalg RSA -keysize 3072 -validity 10000 \
  -storepass "$PASS" -keypass "$PASS" -dname "CN=Turbo Button"
echo "Created turbobutton.jks. Do NOT commit it. Base64 for the GitHub secret:"
base64 -w0 turbobutton.jks; echo
