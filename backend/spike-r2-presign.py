#!/usr/bin/env python3
"""Ask R2 directly what a presigned PUT enforces (V3-D54, the reproducer).

Cloudflare states plainly that presigned POST is not supported on R2, so the
standard `content-length-range` policy does not exist there, and whether R2
enforces a *signed* `Content-Length` on a PUT is documented nowhere. It could not
be settled from documentation, so it was measured against the real bucket on
2026-09-02. This script is that measurement, kept in the repo so the answer can be
re-checked rather than remembered.

The recorded result (2026-09-02):

    0  does the SDK sign it?                                content-length;content-type;host — yes
    1  signed 1 KiB, sends 1 KiB                            200, 1024 stored
    2  signed 1 KiB, declares and sends 64 KiB              403 SignatureDoesNotMatch
    3  signed 1 KiB, declares 1 KiB, sends 64 KiB           200 — and 1024 stored
    4  nothing signed but the type, sends 64 KiB            200, 65536 stored
    5  signed image/png, sends video/mp4                    403 SignatureDoesNotMatch

So the bucket cannot be filled beyond the signed size, and the content-type
allow-list is enforced at R2 rather than by us. ⚠ The mechanism is TRUNCATION, not
refusal: R2 reads exactly Content-Length bytes off the wire, discards the rest and
answers 200, which is why the claim step is a presence-and-cap check rather than an
integrity check.

⚠ Probe 4 is the one to remember. Drop ContentLength from the presign call — as a
simplification, or through an SDK bump — and the bucket becomes an open upload
endpoint with no error anywhere. The Go test that stands in for it is
TestPresignPutSignsContentLength, which asserts `content-length` appears in
X-Amz-SignedHeaders before any upload is attempted.

Usage (it writes real objects and deletes them again):

    pip install boto3
    export STATUS_R2_TEST_ENDPOINT=https://<account>.r2.cloudflarestorage.com
    export STATUS_R2_TEST_BUCKET=ws-tilcer-status-feedback
    export STATUS_R2_TEST_ACCESS_KEY_ID=...
    export STATUS_R2_TEST_SECRET_ACCESS_KEY=...
    python spike-r2-presign.py
"""

import os
import sys
import time
from urllib.parse import urlparse, parse_qs

try:
    import boto3
    import requests
except ImportError:  # pragma: no cover - the script is run by hand
    sys.exit("pip install boto3 requests")

KIB = 1024
PREFIX = "feedback/_spike/"


def client():
    missing = [
        name
        for name in (
            "STATUS_R2_TEST_ENDPOINT",
            "STATUS_R2_TEST_BUCKET",
            "STATUS_R2_TEST_ACCESS_KEY_ID",
            "STATUS_R2_TEST_SECRET_ACCESS_KEY",
        )
        if not os.environ.get(name)
    ]
    if missing:
        sys.exit("missing: " + ", ".join(missing))
    return boto3.client(
        "s3",
        endpoint_url=os.environ["STATUS_R2_TEST_ENDPOINT"],
        aws_access_key_id=os.environ["STATUS_R2_TEST_ACCESS_KEY_ID"],
        aws_secret_access_key=os.environ["STATUS_R2_TEST_SECRET_ACCESS_KEY"],
        region_name="auto",
    )


def presign(s3, bucket, key, content_type, content_length=None):
    params = {"Bucket": bucket, "Key": key, "ContentType": content_type}
    if content_length is not None:
        params["ContentLength"] = content_length
    return s3.generate_presigned_url("put_object", Params=params, ExpiresIn=300)


def stored_size(s3, bucket, key):
    try:
        return s3.head_object(Bucket=bucket, Key=key)["ContentLength"]
    except Exception:
        return None


def main():
    s3 = client()
    bucket = os.environ["STATUS_R2_TEST_BUCKET"]
    run = str(int(time.time()))
    keys = []

    def key_for(n):
        k = f"{PREFIX}{run}/probe{n}.bin"
        keys.append(k)
        return k

    # 0 — does the SDK sign content-length at all?
    url = presign(s3, bucket, key_for(0), "image/png", KIB)
    signed = parse_qs(urlparse(url).query).get("X-Amz-SignedHeaders", [""])[0]
    print(f"0  signed headers: {signed}")

    # 1 — honest: signed 1 KiB, declares and sends 1 KiB.
    k = key_for(1)
    url = presign(s3, bucket, k, "image/png", KIB)
    r = requests.put(url, data=b"a" * KIB, headers={"Content-Type": "image/png", "Content-Length": str(KIB)})
    print(f"1  honest 1 KiB          -> {r.status_code}, stored {stored_size(s3, bucket, k)}")

    # 2 — declares AND sends more than was signed.
    k = key_for(2)
    url = presign(s3, bucket, k, "image/png", KIB)
    r = requests.put(url, data=b"b" * (64 * KIB), headers={"Content-Type": "image/png", "Content-Length": str(64 * KIB)})
    print(f"2  declares 64 KiB       -> {r.status_code}, stored {stored_size(s3, bucket, k)}")

    # 3 — the actual attack: declares the signed size, sends more.
    k = key_for(3)
    url = presign(s3, bucket, k, "image/png", KIB)
    r = requests.put(url, data=b"c" * (64 * KIB), headers={"Content-Type": "image/png", "Content-Length": str(KIB)})
    print(f"3  lies about length     -> {r.status_code}, stored {stored_size(s3, bucket, k)}")

    # 4 — ⚠ what happens with no signed length at all.
    k = key_for(4)
    url = presign(s3, bucket, k, "image/png")
    r = requests.put(url, data=b"d" * (64 * KIB), headers={"Content-Type": "image/png"})
    print(f"4  no signed length      -> {r.status_code}, stored {stored_size(s3, bucket, k)}  <-- the open endpoint")

    # 5 — a content type other than the signed one.
    k = key_for(5)
    url = presign(s3, bucket, k, "image/png", KIB)
    r = requests.put(url, data=b"e" * KIB, headers={"Content-Type": "video/mp4", "Content-Length": str(KIB)})
    print(f"5  wrong content type    -> {r.status_code}, stored {stored_size(s3, bucket, k)}")

    for k in keys:
        try:
            s3.delete_object(Bucket=bucket, Key=k)
        except Exception as exc:  # pragma: no cover
            print(f"   could not clean up {k}: {exc}")


if __name__ == "__main__":
    main()
