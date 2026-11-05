#!/bin/sh
set -eu
out=${1:-rex-micro-signing.p12}
pass=${MACOS_CERT_PASSWORD:-$(openssl rand -hex 16)}
tmp=$(mktemp -d)
cat > "$tmp/cert.cnf" <<CNF
[req]
distinguished_name=dn
x509_extensions=ext
prompt=no
[dn]
CN=rex-micro self-signed
[ext]
basicConstraints=critical,CA:false
keyUsage=critical,digitalSignature
extendedKeyUsage=critical,codeSigning
CNF
openssl req -x509 -newkey rsa:3072 -nodes -keyout "$tmp/key.pem" -out "$tmp/cert.pem" -days 7300 -config "$tmp/cert.cnf"
openssl pkcs12 -export -legacy -inkey "$tmp/key.pem" -in "$tmp/cert.pem" -out "$out" -passout "pass:$pass"
rm -rf "$tmp"
echo "wrote $out"
echo "MACOS_CERT_PASSWORD=$pass"
echo "MACOS_CERT_P12: base64 -i $out | pbcopy"
