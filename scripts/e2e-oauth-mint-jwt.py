#!/usr/bin/env python3
"""Mint a throwaway RSA keypair/JWKS/JWT for the OAuth e2e harness.

Not a production auth tool: this signs self-issued RS256 tokens against a
locally-generated key so scripts/e2e-oauth-docker.sh can exercise the real
OAuth code path (internal/auth/middleware.go) without a hosted IdP.

Usage:
  e2e-oauth-mint-jwt.py init  <dir> <kid>
      Generates an RSA keypair + JWKS document into <dir>/priv.pem and
      <dir>/jwks.json.
  e2e-oauth-mint-jwt.py mint  <dir> <subject> <kid> <issuer> <audience>
      Signs a JWT for <subject> using <dir>/priv.pem, printed to stdout.
"""
import base64
import json
import sys
import time

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa


def b64url_uint(n):
    b = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def cmd_init(directory, kid):
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    priv_pem = key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    pub = key.public_key().public_numbers()
    jwks = {
        "keys": [
            {
                "kty": "RSA",
                "use": "sig",
                "alg": "RS256",
                "kid": kid,
                "n": b64url_uint(pub.n),
                "e": b64url_uint(pub.e),
            }
        ]
    }
    with open(f"{directory}/priv.pem", "wb") as f:
        f.write(priv_pem)
    with open(f"{directory}/jwks.json", "w") as f:
        json.dump(jwks, f, indent=2)


def cmd_mint(directory, subject, kid, issuer, audience):
    with open(f"{directory}/priv.pem", "rb") as f:
        priv_pem = f.read()
    now = int(time.time())
    claims = {
        "iss": issuer,
        "aud": audience,
        "sub": subject,
        "tenant_id": "default",
        "iat": now,
        "exp": now + 3600,
    }
    token = jwt.encode(claims, priv_pem, algorithm="RS256", headers={"kid": kid})
    print(token)


def main():
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    mode, directory = sys.argv[1], sys.argv[2]
    if mode == "init":
        cmd_init(directory, sys.argv[3])
    elif mode == "mint":
        cmd_mint(directory, sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6])
    else:
        print(f"unknown mode: {mode}", file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
